package tui

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
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
		owner   any
		results []fetchResult
	}
	pulledAllMsg struct { // P finished; one pulledMsg per repository
		owner   any
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
func fetchAll(owner any, reader GitReader, targets []repoTarget) tea.Cmd {
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
func pullAll(owner any, reader GitReader, targets []repoTarget) tea.Cmd {
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

// sessionRepos returns the repositories of the session's projects. If there
// are none, or a git action is running, it sets a notice saying why.
func (m *runningModel) sessionRepos() ([]repoTarget, bool) {
	switch {
	case m.stopping:
		return nil, false
	case m.git == nil:
		m.notice = "git is not installed"
		return nil, false
	case m.gitAction != "":
		m.notice = m.gitAction + " is still running"
		return nil, false
	}
	names := make([]string, len(m.entries))
	for i, e := range m.entries {
		names[i] = e.info.Name
	}
	targets := m.git.reposOf(names)
	if len(targets) == 0 {
		m.notice = "no project in this session is in a git repository"
	}
	return targets, len(targets) > 0
}

// fetchAllRepos fetches every repository of the session (F).
func (m *runningModel) fetchAllRepos() tea.Cmd {
	targets, ok := m.sessionRepos()
	if !ok {
		return nil
	}
	m.gitAction = "git fetch"
	m.notice = "fetching " + count(len(targets), "repo") + "…"
	return fetchAll(m, m.git.reader, targets)
}

// fetchedAll reports fetches in the footer, and failures in the logs of
// their repositories' projects, then refreshes the git state so the ↑ ↓
// markers are current.
func (m *runningModel) fetchedAll(msg fetchedAllMsg) tea.Cmd {
	m.gitAction = ""
	m.notice = fetchSummary(msg.results)
	failed := false
	for _, r := range msg.results {
		if r.err != nil {
			failed = true
			for _, name := range r.target.members {
				_ = m.sess.Note(name, fetchLines(r)...)
			}
		}
	}
	if failed {
		m.notice += " (output in the logs)"
	}
	return m.git.refresh(m.gitDirs())
}

// confirmPullAll asks whether to pull every repository of the session (P).
func (m *runningModel) confirmPullAll() {
	targets, ok := m.sessionRepos()
	if !ok {
		return
	}
	m.confirm = pullAllQuestion(targets, m.labelOf, func() tea.Cmd {
		if m.gitAction != "" {
			m.notice = m.gitAction + " is still running"
			return nil
		}
		m.gitAction = "git pull"
		m.notice = "pulling " + count(len(targets), "repo") + "…"
		return pullAll(m, m.git.reader, targets)
	})
}

// pulledAll reports pulls in the footer and each repository's outcome in
// the logs of its projects: git's output in the first one's, a line in the
// others'. Nothing is restarted.
func (m *runningModel) pulledAll(msg pulledAllMsg) tea.Cmd {
	m.gitAction = ""
	m.notice = pullSummary(msg.results)
	for i, r := range msg.results {
		lines := pullAllLines(r)
		first := msg.members[i][0]
		_ = m.sess.Note(first, lines...)
		for _, other := range msg.members[i][1:] {
			_ = m.sess.Note(other, fmt.Sprintf("%s (git pull run for %s, same repository)", lines[len(lines)-1], m.labelOf(first)))
		}
	}
	return m.git.refresh(m.gitDirs())
}

// launcherRepos returns the repositories of the named projects. If there
// are none, or a git action is running, it sets a notice saying why.
func (m *launcherModel) launcherRepos(names []string, none string) ([]repoTarget, bool) {
	switch {
	case m.git == nil:
		m.notice = "git is not installed"
		return nil, false
	case m.gitAction != "":
		m.notice = m.gitAction + " is still running"
		return nil, false
	}
	targets := m.git.reposOf(names)
	if len(targets) == 0 {
		m.notice = none
	}
	return targets, len(targets) > 0
}

// fetchAllRepos fetches the repository of every registered project (F).
func (m *launcherModel) fetchAllRepos() tea.Cmd {
	targets, ok := m.launcherRepos(slices.Sorted(maps.Keys(m.cfg.Projects)), "no project is in a git repository")
	if !ok {
		return nil
	}
	m.gitAction = "git fetch"
	m.notice = "fetching " + count(len(targets), "repo") + "…"
	return fetchAll(m, m.git.reader, targets)
}

// fetchedAll reports fetches in the help line, and git's output for the
// failed ones in place of the list.
func (m *launcherModel) fetchedAll(msg fetchedAllMsg) {
	m.gitAction, m.gitStale, m.skipped = "", true, false
	m.notice = fetchSummary(msg.results)
	var lines []string
	for _, r := range msg.results {
		if r.err != nil {
			lines = append(lines, strings.Join(r.target.members, ", ")+":")
			lines = append(lines, fetchLines(r)...)
		}
	}
	if lines != nil {
		m.output = lines
	}
}

// confirmPullAll asks whether to pull the repositories of the selection, or
// of the group or project under the cursor (P).
func (m *launcherModel) confirmPullAll() {
	names := m.selection()
	what := "the selection"
	if len(names) == 0 {
		it, ok := m.current()
		if !ok {
			return
		}
		names, what = []string{it.name}, it.name
	}
	var projects []string
	for _, n := range names {
		if members, ok := m.cfg.Groups[n]; ok {
			projects = append(projects, members...)
		} else {
			projects = append(projects, n)
		}
	}
	targets, ok := m.launcherRepos(projects, "nothing in "+what+" is in a git repository")
	if !ok {
		return
	}
	m.ask = pullAllQuestion(targets, func(s string) string { return s }, func() tea.Cmd {
		if m.gitAction != "" {
			m.notice = m.gitAction + " is still running"
			return nil
		}
		m.gitAction = "git pull"
		m.notice = "pulling " + count(len(targets), "repo") + "…"
		return pullAll(m, m.git.reader, targets)
	})
}

// pulledAll reports pulls in the help line, and the repositories that were
// skipped or failed, with why, in place of the list.
func (m *launcherModel) pulledAll(msg pulledAllMsg) {
	m.gitAction, m.gitStale = "", true
	m.notice = pullSummary(msg.results)
	var lines []string
	m.skipped = true
	for i, r := range msg.results {
		if o := pullOutcome(r); o == "skipped" || o == "failed" {
			lines = append(lines, strings.Join(msg.members[i], ", ")+":")
			lines = append(lines, pullAllLines(r)...)
			m.skipped = m.skipped && o == "skipped"
		}
	}
	if lines != nil {
		m.output = lines
	}
}
