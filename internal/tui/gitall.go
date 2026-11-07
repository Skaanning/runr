package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/gitinfo"
)

// F fetches and P pulls every repository at once: those of the session in
// the running view, those of every registered project (F) or of the
// selection (P) in the launcher. runr never fetches on its own.

const maxFetches = 4 // repositories fetched at the same time

// repoTarget is a repository and the projects in it that an action covers.
type repoTarget struct {
	repo    gitinfo.Repo
	members []string
}

type (
	fetchResult struct {
		target repoTarget
		output string
		err    error
	}
	fetchedAllMsg struct { // F finished
		owner   *gitActions
		results []fetchResult
	}
	pulledAllMsg struct { // P finished; one pulledMsg per repository
		owner   *gitActions
		results []pulledMsg
		members [][]string // the projects of each result's repository
	}
)

// reposOf returns the repositories of the named projects, each once, in the
// order of their first project. Projects outside a repository, or whose
// state couldn't be read, are left out.
func (t *gitTracker) reposOf(names []string) []repoTarget {
	if t == nil {
		return nil
	}
	var out []repoTarget
	index := map[string]int{}
	for _, name := range names {
		g, ok := t.get(name)
		if !ok || g.err != nil {
			continue
		}
		i, ok := index[g.root]
		if !ok {
			i = len(out)
			index[g.root] = i
			out = append(out, repoTarget{repo: t.repos.ByRoot[g.root]})
		}
		out[i].members = append(out[i].members, name)
	}
	return out
}

// fetchAll fetches the repositories in the background, maxFetches at a
// time.
func fetchAll(owner *gitActions, reader GitReader, targets []repoTarget) tea.Cmd {
	return func() tea.Msg {
		results := make([]fetchResult, len(targets))
		sem := make(chan struct{}, maxFetches)
		var wg sync.WaitGroup
		for i, tg := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				out, err := reader.Fetch(context.Background(), tg.repo)
				results[i] = fetchResult{target: tg, output: out, err: err}
			}()
		}
		wg.Wait()
		return fetchedAllMsg{owner: owner, results: results}
	}
}

// fetchSummary sums up fetches for the footer, e.g. "fetched 3 repos ·
// 1 failed".
func fetchSummary(results []fetchResult) string {
	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
		}
	}
	s := "fetched " + count(len(results)-failed, "repo")
	if failed > 0 {
		s += fmt.Sprintf(" · %d failed", failed)
	}
	return s
}

// fetchLines returns what a failed fetch printed, after the command.
func fetchLines(r fetchResult) []string {
	lines := append([]string{"$ git " + strings.Join(gitinfo.FetchArgs(), " ")}, outputLines(r.output)...)
	return append(lines, "git fetch failed: "+r.err.Error())
}

// pullAll checks and pulls the repositories one at a time in the
// background, like a single pull each (see runPull); those that fail the
// check are skipped.
func pullAll(owner *gitActions, reader GitReader, targets []repoTarget) tea.Cmd {
	return func() tea.Msg {
		msg := pulledAllMsg{owner: owner}
		for _, tg := range targets {
			msg.results = append(msg.results, runPull(owner, reader, tg.members[0], tg.repo)().(pulledMsg))
			msg.members = append(msg.members, tg.members)
		}
		return msg
	}
}

// pullOutcome classifies a finished pull: "pulled", "up to date",
// "skipped" (the check refused) or "failed".
func pullOutcome(msg pulledMsg) string {
	switch {
	case !msg.ran:
		return "skipped"
	case msg.err != nil:
		return "failed"
	case pullChange(msg) == "":
		return "up to date"
	}
	return "pulled"
}

// pullSummary sums up pulls for the footer, e.g. "pulled 2 · up to date 1 ·
// skipped 1"; outcomes that didn't occur are left out.
func pullSummary(results []pulledMsg) string {
	n := map[string]int{}
	for _, r := range results {
		n[pullOutcome(r)]++
	}
	var parts []string
	for _, o := range []string{"pulled", "up to date", "skipped", "failed"} {
		if n[o] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", o, n[o]))
		}
	}
	return strings.Join(parts, " · ")
}

// pullAllLines describes one repository's pull of P: git's output for one
// that ran, why it was skipped otherwise.
func pullAllLines(msg pulledMsg) []string {
	switch pullOutcome(msg) {
	case "skipped":
		return []string{"git pull skipped: " + msg.err.Error()}
	case "failed":
		return append(pullLines(msg), "git pull failed")
	case "up to date":
		return append(pullLines(msg), "already up to date")
	}
	return append(pullLines(msg), "pulled "+pullChange(msg))
}

// pullAllQuestion asks whether to pull the repositories, listing each with
// its projects (shown with label), e.g. "Pull 2 repos? app (api, Worker) ·
// infra (db)".
func pullAllQuestion(targets []repoTarget, label func(string) string, yes func() tea.Cmd) *question {
	parts := make([]string, len(targets))
	for i, tg := range targets {
		labels := make([]string, len(tg.members))
		for j, m := range tg.members {
			labels[j] = label(m)
		}
		parts[i] = filepath.Base(tg.repo.Root) + " (" + strings.Join(labels, ", ") + ")"
	}
	return &question{
		prompt: "Pull " + count(len(targets), "repo") + "? " + strings.Join(parts, " · "),
		action: "pull",
		yes:    yes,
	}
}

// repos returns the repositories of the named projects. If there are none
// (none says so), or a git action is running, it sets a notice saying why.
func (a *gitActions) repos(names []string, none string) ([]repoTarget, bool) {
	switch {
	case a.host.tracker() == nil:
		a.host.setNotice("git is not installed")
		return nil, false
	case a.action != "":
		a.host.setNotice(a.action + " is still running")
		return nil, false
	}
	targets := a.host.tracker().reposOf(names)
	if len(targets) == 0 {
		a.host.setNotice(none)
	}
	return targets, len(targets) > 0
}

// fetchAllRepos fetches the repositories of the named projects (F); none
// is the notice if none of them is in one.
func (a *gitActions) fetchAllRepos(names []string, none string) tea.Cmd {
	targets, ok := a.repos(names, none)
	if !ok {
		return nil
	}
	a.action = "git fetch"
	a.host.setNotice("fetching " + count(len(targets), "repo") + "…")
	return fetchAll(a, a.host.tracker().reader, targets)
}

// fetchedAll reports fetches in the help line, and git's output for the
// failed ones in the logs of their repositories' projects (or in place of
// the list), then refreshes the git state so the ↑ ↓ markers are current.
func (a *gitActions) fetchedAll(msg fetchedAllMsg) tea.Cmd {
	h := a.host
	a.action = ""
	notice := fetchSummary(msg.results)
	var out []string
	logged := false
	for _, r := range msg.results {
		if r.err == nil {
			continue
		}
		lines := fetchLines(r)
		for _, name := range r.target.members {
			logged = h.note(name, lines...) || logged
		}
		out = append(out, strings.Join(r.target.members, ", ")+":")
		out = append(out, lines...)
	}
	if logged {
		notice += " (output in the logs)"
	}
	h.setNotice(notice)
	if out != nil {
		h.showOutput(out, false)
	}
	return h.refresh()
}

// confirmPullAll asks whether to pull the repositories of the named
// projects (P); none is the notice if none of them is in one.
func (a *gitActions) confirmPullAll(names []string, none string) {
	targets, ok := a.repos(names, none)
	if !ok {
		return
	}
	a.host.askQuestion(pullAllQuestion(targets, a.host.label, func() tea.Cmd {
		if !a.begin("git pull") {
			return nil
		}
		a.host.setNotice("pulling " + count(len(targets), "repo") + "…")
		return pullAll(a, a.host.tracker().reader, targets)
	}))
}

// pulledAll reports pulls in the help line and each repository's outcome
// in the logs of its projects: git's output in the first one's, a line in
// the others'. The screens without logs show the repositories that were
// skipped or failed, with why, in place of the list. Nothing is restarted.
func (a *gitActions) pulledAll(msg pulledAllMsg) tea.Cmd {
	h := a.host
	a.action = ""
	h.setNotice(pullSummary(msg.results))
	var out []string
	skippedOnly := true
	for i, r := range msg.results {
		lines := pullAllLines(r)
		first := msg.members[i][0]
		h.note(first, lines...)
		for _, other := range msg.members[i][1:] {
			h.note(other, sameRepo(lines[len(lines)-1], "git pull", h.label(first)))
		}
		if o := pullOutcome(r); o == "skipped" || o == "failed" {
			out = append(out, strings.Join(msg.members[i], ", ")+":")
			out = append(out, lines...)
			skippedOnly = skippedOnly && o == "skipped"
		}
	}
	if out != nil {
		h.showOutput(out, skippedOnly)
	}
	return h.refresh()
}
