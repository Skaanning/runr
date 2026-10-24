package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"runr/internal/gitinfo"
)

// newGitModel returns a running view where api and Worker share the clean
// repository /r/app (on main), and db is in /r/infra.
func newGitModel(t *testing.T) (*runningModel, *fakeSession, *fakeGit) {
	t.Helper()
	m, sess := newTestModel(t, 120, 14)
	f := testGit()
	f.status["/r/app"] = gitinfo.Status{Branch: "main", Commit: "0123456789"}
	f.branches = map[string][]gitinfo.Branch{"/r/app": {
		{Name: "main", Current: true, Upstream: "origin/main", Date: "3 days ago"},
		{Name: "feat/login", Upstream: "origin/feat/login", Date: "2 hours ago"},
		{Name: "origin/fix/typo", Remote: true, Date: "5 days ago"},
	}}
	m.git = newGitTracker(f)
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
	m.Update(key("down")) // select api
	return m, sess, f
}

// press sends a key and runs the resulting command chain synchronously,
// stopping at commands that would wait (ticks, cursor blinks).
func press(t *testing.T, m *runningModel, k string) {
	t.Helper()
	_, cmd := m.Update(key(k))
	follow(t, m, cmd)
}

func follow(t *testing.T, m *runningModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case branchesMsg, switchedMsg:
		_, next := m.Update(msg)
		follow(t, m, next)
	case tea.BatchMsg:
		for _, c := range msg {
			follow(t, m, c)
		}
	}
	// Anything else (git refreshes, ticks, input focus) is not followed.
}

func TestSwitchBranch(t *testing.T) {
	m, sess, f := newGitModel(t)
	if got := helpRow(m); !strings.Contains(got, "b branch") {
		t.Errorf("help = %q", got)
	}

	press(t, m, "b")
	if m.picker == nil {
		t.Fatalf("no picker; notice %q", m.notice)
	}
	rows := screen(m)
	for _, want := range []string{
		"switch",
		"api is on main · Worker shares the repository and switches too",
		"▸ main        current · 3 days ago",
		"  feat/login  2 hours ago",
		"  fix/typo    new branch from origin/fix/typo · 5 days ago",
	} {
		if !slices.ContainsFunc(rows, func(r string) bool { return strings.Contains(r, want) }) {
			t.Errorf("screen lacks %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if got := helpRow(m); got != " type to filter  ↑↓ select  enter switch  esc cancel" {
		t.Errorf("picker help = %q", got)
	}

	for _, k := range []string{"t", "y", "p", "o"} {
		press(t, m, k)
	}
	f.switchOut = "branch 'fix/typo' set up to track 'origin/fix/typo'.\nSwitched to a new branch 'fix/typo'\n"
	_, cmd := m.Update(key("enter"))
	if m.gitAction != "git switch" || m.notice != "switching api to fix/typo…" {
		t.Errorf("while switching: action %q, notice %q", m.gitAction, m.notice)
	}
	follow(t, m, cmd)

	if !slices.Equal(f.switches, []string{"/r/app origin/fix/typo"}) {
		t.Errorf("switches = %v", f.switches)
	}
	if m.notice != "switched api to fix/typo (also Worker)" || m.gitAction != "" {
		t.Errorf("notice %q, action %q", m.notice, m.gitAction)
	}
	wantLog := []string{
		"$ git switch --track origin/fix/typo",
		"branch 'fix/typo' set up to track 'origin/fix/typo'.",
		"Switched to a new branch 'fix/typo'",
		"switched to fix/typo",
	}
	if got := sess.notes["api"]; !slices.Equal(got, wantLog) {
		t.Errorf("api log = %q", got)
	}
	if got := sess.notes["worker"]; !slices.Equal(got, []string{"switched to fix/typo (git switch run for api, same repository)"}) {
		t.Errorf("worker log = %q", got)
	}
	if sess.notes["db"] != nil {
		t.Error("db's log got the output of another repository")
	}
	if !m.git.busy {
		t.Error("git state not refreshed after the switch")
	}
}

func TestSwitchBranchRefusedUpFront(t *testing.T) {
	m, _, f := newGitModel(t)
	f.status["/r/app"] = gitinfo.Status{Branch: "main", Changed: true}
	press(t, m, "b")
	if m.picker != nil || m.notice != "can't switch branches: there are uncommitted changes to tracked files" {
		t.Errorf("picker %v, notice %q", m.picker != nil, m.notice)
	}

	f.status["/r/app"] = gitinfo.Status{Branch: "main", Operation: gitinfo.Merge, Conflicts: true}
	press(t, m, "b")
	if m.notice != "can't switch branches: a merge is in progress" {
		t.Errorf("notice %q", m.notice)
	}
	if len(f.switches) != 0 {
		t.Error("switched anyway")
	}
}

func TestSwitchBranchCheckedAgainBeforeSwitching(t *testing.T) {
	m, sess, f := newGitModel(t)
	press(t, m, "b")
	press(t, m, "down")
	f.status["/r/app"] = gitinfo.Status{Branch: "main", Changed: true} // edited while the picker was open
	press(t, m, "enter")
	if len(f.switches) != 0 || len(sess.notes) != 0 {
		t.Errorf("switched: %v, notes %v", f.switches, sess.notes)
	}
	if m.notice != "can't switch branches: there are uncommitted changes to tracked files" || m.gitAction != "" {
		t.Errorf("notice %q, action %q", m.notice, m.gitAction)
	}
}

func TestSwitchBranchFailure(t *testing.T) {
	m, sess, f := newGitModel(t)
	f.switchOut = "error: The following untracked working tree files would be overwritten by checkout:\n\tnew.txt\nAborting\n"
	f.switchErr = &gitinfo.Error{Args: []string{"switch", "feat/login"}, Stderr: f.switchOut, Err: errors.New("exit status 1")}
	press(t, m, "b")
	press(t, m, "down")
	press(t, m, "enter")

	if want := "git switch: error: The following untracked working tree files would be overwritten by checkout: (output in the log)"; m.notice != want {
		t.Errorf("notice = %q", m.notice)
	}
	if got := sess.notes["api"]; len(got) != 5 || got[0] != "$ git switch feat/login" || got[2] != "\tnew.txt" || got[4] != "git switch failed" {
		t.Errorf("log = %q", got)
	}
	if sess.notes["worker"] != nil {
		t.Error("a failed switch was noted for the other projects in the repository")
	}
}

func TestSwitchBranchGuards(t *testing.T) {
	m, _, _ := newGitModel(t)
	press(t, m, "b")
	press(t, m, "enter") // the current branch
	if m.notice != "already on main" {
		t.Errorf("notice = %q", m.notice)
	}

	m.gitAction = "git switch"
	press(t, m, "b")
	if m.notice != "git switch is still running" || m.picker != nil {
		t.Errorf("busy: notice %q", m.notice)
	}
	m.gitAction = ""

	m.git.state["api"] = projectGit{err: errors.New("git rev-parse: fatal: detected dubious ownership")}
	press(t, m, "b")
	if m.notice != "git rev-parse: fatal: detected dubious ownership" {
		t.Errorf("error: notice %q", m.notice)
	}

	delete(m.git.state, "api")
	press(t, m, "b")
	if m.notice != "api is not in a git repository" {
		t.Errorf("no repo: notice %q", m.notice)
	}
	if strings.Contains(helpRow(m), "b branch") {
		t.Error("b hint shown for a project outside a repository")
	}

	m.git = nil
	press(t, m, "b")
	if m.notice != "git is not installed" {
		t.Errorf("no git: notice %q", m.notice)
	}
}

func TestOutputLines(t *testing.T) {
	got := outputLines("Updating files:  50% (1/2)\rUpdating files: 100% (2/2), done.\r\nSwitched to branch 'x'\n\n")
	want := []string{"Updating files: 100% (2/2), done.", "Switched to branch 'x'"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}
