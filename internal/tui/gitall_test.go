package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/gitinfo"
)

func TestFetchAll(t *testing.T) {
	m, sess, f := newGitModel(t)
	m.Update(tea.WindowSizeMsg{Width: 250, Height: 14}) // room for every hint
	if got := helpRow(m); !strings.Contains(got, "p  pull  F  fetch all  P  pull all") {
		t.Errorf("help = %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	f.fetchedTo = map[string]gitinfo.Status{"/r/app": {Branch: "main", Commit: "0123456789", Upstream: "origin/main", Behind: 3}}

	_, cmd := m.Update(key("F"))
	if m.notice != "fetching 2 repos…" || m.gitOps.action != "git fetch" {
		t.Errorf("notice %q, action %q", m.notice, m.gitOps.action)
	}
	follow(t, m, cmd)
	slices.Sort(f.fetches)
	if !slices.Equal(f.fetches, []string{"/r/app", "/r/infra"}) {
		t.Errorf("fetched %q", f.fetches)
	}
	if m.notice != "fetched 2 repos" || m.gitOps.action != "" || len(sess.notes) != 0 {
		t.Errorf("notice %q, action %q, notes %q", m.notice, m.gitOps.action, sess.notes)
	}
	// The git state is read again, so the markers are current (follow
	// doesn't run that refresh; run one here).
	if !m.git.busy {
		t.Error("no refresh after the fetch")
	}
	m.git.busy = false
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
	if g, _ := m.git.get("api"); g.status.Behind != 3 {
		t.Errorf("api's status after the fetch: %+v", g.status)
	}

	// Failures go to the logs of the repository's projects.
	f.fetchErrs = map[string]error{"/r/app": errors.New("git fetch: fatal: could not read from remote repository")}
	f.fetchOut = "ssh: connect to host example.com: Connection refused\n"
	press(t, m, "F")
	if m.notice != "fetched 1 repo · 1 failed (output in the logs)" {
		t.Errorf("notice %q", m.notice)
	}
	want := []string{
		"$ git fetch", "ssh: connect to host example.com: Connection refused",
		"git fetch failed: git fetch: fatal: could not read from remote repository",
	}
	if !slices.Equal(sess.notes["api"], want) || !slices.Equal(sess.notes["worker"], want) || sess.notes["db"] != nil {
		t.Errorf("notes = %q", sess.notes)
	}
}

func TestFetchAllBusy(t *testing.T) {
	m, _, f := newGitModel(t)
	m.gitOps.action = "git pull"
	press(t, m, "F")
	press(t, m, "P")
	if m.notice != "git pull is still running" || len(f.fetches) != 0 || m.confirm != nil {
		t.Errorf("notice %q, fetches %q, question %v", m.notice, f.fetches, m.confirm != nil)
	}
	m.gitOps.action, m.git = "", nil
	press(t, m, "F")
	if m.notice != "git is not installed" {
		t.Errorf("without git: %q", m.notice)
	}
}

func TestPullAll(t *testing.T) {
	m, sess, f := newGitModel(t)
	f.status["/r/infra"] = gitinfo.Status{Branch: "main", Commit: "a1b2c3d4e5", Upstream: "origin/main", Changed: true}
	f.pulledTo = map[string]gitinfo.Status{"/r/app": {Branch: "main", Commit: "9d7ae2a000", Upstream: "origin/main"}}
	f.pullOut = "Updating 0123456..9d7ae2a\nFast-forward\n"

	press(t, m, "P")
	if m.confirm == nil {
		t.Fatalf("no question; notice %q", m.notice)
	}
	if got := helpRow(m); !strings.HasPrefix(got, " Pull 2 repos? infra (db) · app (api, Worker)") {
		t.Errorf("question = %q", got)
	}
	press(t, m, "y")
	if !slices.Equal(f.pulls, []string{"/r/app"}) {
		t.Errorf("pulled %q", f.pulls)
	}
	if m.notice != "pulled 1 · skipped 1" || m.gitOps.action != "" {
		t.Errorf("notice %q, action %q", m.notice, m.gitOps.action)
	}
	wantAPI := []string{"$ git pull --ff-only --no-rebase", "Updating 0123456..9d7ae2a", "Fast-forward", "pulled main 0123456 → 9d7ae2a"}
	if !slices.Equal(sess.notes["api"], wantAPI) {
		t.Errorf("api notes = %q", sess.notes["api"])
	}
	if want := []string{"pulled main 0123456 → 9d7ae2a (git pull run for api, same repository)"}; !slices.Equal(sess.notes["worker"], want) {
		t.Errorf("worker notes = %q", sess.notes["worker"])
	}
	if want := []string{"git pull skipped: there are uncommitted changes to tracked files"}; !slices.Equal(sess.notes["db"], want) {
		t.Errorf("db notes = %q", sess.notes["db"])
	}
	if len(sess.calls) != 0 {
		t.Errorf("session calls %q (nothing is restarted)", sess.calls)
	}

	// esc cancels; nothing is pulled.
	f.pulls = nil
	press(t, m, "P")
	press(t, m, "esc")
	if m.confirm != nil || len(f.pulls) != 0 {
		t.Errorf("cancelled: pulls %q", f.pulls)
	}
}

func TestPullSummary(t *testing.T) {
	up := pulledMsg{ran: true, before: gitinfo.Status{Commit: "a"}, after: gitinfo.Status{Commit: "a"}}
	moved := pulledMsg{ran: true, before: gitinfo.Status{Branch: "main", Commit: "a"}, after: gitinfo.Status{Branch: "main", Commit: "b"}}
	failed := pulledMsg{ran: true, err: errors.New("x")}
	skipped := pulledMsg{err: errors.New("y")}
	if got := pullSummary([]pulledMsg{moved, up, skipped, moved, failed}); got != "pulled 2 · up to date 1 · skipped 1 · failed 1" {
		t.Errorf("got %q", got)
	}
	if got := fetchSummary([]fetchResult{{}}); got != "fetched 1 repo" {
		t.Errorf("got %q", got)
	}
}

func TestLauncherFetchAll(t *testing.T) {
	m, f := newGitLauncher(t)
	f.fetchErrs = map[string]error{"/r/infra": errors.New("git fetch: fatal: 'origin' does not appear to be a git repository")}
	launcherPress(t, m, "F")
	slices.Sort(f.fetches)
	if !slices.Equal(f.fetches, []string{"/r/app", "/r/infra"}) {
		t.Errorf("fetched %q", f.fetches) // every registered project's repository
	}
	if m.notice != "fetched 1 repo · 1 failed" || m.gitOps.action != "" {
		t.Errorf("notice %q", m.notice)
	}
	want := []string{"db:", "$ git fetch", "git fetch failed: git fetch: fatal: 'origin' does not appear to be a git repository"}
	if !slices.Equal(m.output, want) {
		t.Errorf("output = %q", m.output)
	}
	screenHas(t, launcherScreen(m), "git fetch failed")
	launcherPress(t, m, "down") // any key
	if m.output != nil {
		t.Error("output kept after a key")
	}
}

func TestLauncherPullAll(t *testing.T) {
	m, f := newGitLauncher(t)
	f.status["/r/infra"] = gitinfo.Status{Branch: "main", Commit: "a1b2c3d4e5"} // no upstream
	// On the group backend (db, api): its repositories.
	launcherPress(t, m, "P")
	if m.ask == nil || !strings.HasPrefix(m.ask.prompt, "Pull 2 repos? infra (db) · app (api)") {
		t.Fatalf("question %+v; notice %q", m.ask, m.notice)
	}
	launcherPress(t, m, "enter")
	if !slices.Equal(f.pulls, []string{"/r/app"}) || m.notice != "up to date 1 · skipped 1" {
		t.Errorf("pulls %q, notice %q", f.pulls, m.notice)
	}
	if want := []string{"db:", "git pull skipped: main has no upstream branch"}; !slices.Equal(m.output, want) {
		t.Errorf("output = %q", m.output)
	}
	if rows := launcherScreen(m); !strings.Contains(rows[len(rows)-2], "SKIPPED") {
		t.Errorf("status bar = %q", rows[len(rows)-2])
	}

	// A selection: just worker's repository.
	f.pulls = nil
	launcherPress(t, m, "esc")
	m.selected = map[string]bool{"worker": true}
	launcherPress(t, m, "P")
	if m.ask == nil || m.ask.prompt != "Pull 1 repo? app (worker)" {
		t.Fatalf("question %+v", m.ask)
	}
	launcherPress(t, m, "y")
	if !slices.Equal(f.pulls, []string{"/r/app"}) || m.output != nil {
		t.Errorf("pulls %q, output %q", f.pulls, m.output)
	}
}
