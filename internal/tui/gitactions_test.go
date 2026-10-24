package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/gitinfo"
)

// newGitModel returns a running view where api and Worker share the clean
// repository /r/app (on main), and db is in /r/infra.
func newGitModel(t *testing.T) (*runningModel, *fakeSession, *fakeGit) {
	t.Helper()
	m, sess := newTestModel(t, 120, 14)
	f := testGit()
	f.status["/r/app"] = gitinfo.Status{Branch: "main", Commit: "0123456789", Upstream: "origin/main"}
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
	case branchesMsg, switchedMsg, pullCheckMsg, pulledMsg:
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

func TestPull(t *testing.T) {
	m, sess, f := newGitModel(t)
	if got := helpRow(m); !strings.Contains(got, "b branch  p pull") {
		t.Errorf("help = %q", got)
	}

	press(t, m, "p")
	if m.confirm == nil {
		t.Fatalf("no confirmation; notice %q", m.notice)
	}
	if got, want := helpRow(m), " pull origin/main into api (main)? also updates Worker (same repository)  enter pull  esc cancel"; got != want {
		t.Errorf("confirmation = %q\nwant           %q", got, want)
	}
	m.width = 40
	if got := helpRow(m); got != " pull origin/ma…  enter pull  esc cancel" {
		t.Errorf("narrow confirmation = %q", got)
	}
	m.width = 120
	if len(f.pulls) != 0 {
		t.Fatal("pulled before confirming")
	}

	f.pullOut = "Updating 0123456..fedcba9\nFast-forward\n f | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n"
	f.pulledTo = map[string]gitinfo.Status{"/r/app": {Branch: "main", Commit: "fedcba9876", Upstream: "origin/main"}}
	_, cmd := m.Update(key("enter"))
	if m.confirm != nil || m.gitAction != "git pull" || m.notice != "pulling api…" {
		t.Errorf("while pulling: confirm %v, action %q, notice %q", m.confirm != nil, m.gitAction, m.notice)
	}
	follow(t, m, cmd)

	if !slices.Equal(f.pulls, []string{"/r/app"}) {
		t.Errorf("pulls = %v", f.pulls)
	}
	if m.notice != "pulled api: main 0123456 → fedcba9 (also Worker)" || m.gitAction != "" {
		t.Errorf("notice %q, action %q", m.notice, m.gitAction)
	}
	wantLog := []string{
		"$ git pull --ff-only --no-rebase",
		"Updating 0123456..fedcba9",
		"Fast-forward",
		" f | 2 +-",
		" 1 file changed, 1 insertion(+), 1 deletion(-)",
		"pulled main 0123456 → fedcba9",
	}
	if got := sess.notes["api"]; !slices.Equal(got, wantLog) {
		t.Errorf("api log = %q", got)
	}
	if got := sess.notes["worker"]; !slices.Equal(got, []string{"pulled main 0123456 → fedcba9 (git pull run for api, same repository)"}) {
		t.Errorf("worker log = %q", got)
	}
	if sess.notes["db"] != nil {
		t.Error("db's log got the output of another repository")
	}
	if !m.git.busy {
		t.Error("git state not refreshed after the pull")
	}
}

func TestPullUpToDate(t *testing.T) {
	m, sess, f := newGitModel(t)
	f.pullOut = "Already up to date.\n"
	press(t, m, "p")
	press(t, m, "y")
	if m.notice != "api is already up to date" {
		t.Errorf("notice %q", m.notice)
	}
	if got := sess.notes["api"]; !slices.Equal(got, []string{"$ git pull --ff-only --no-rebase", "Already up to date.", "already up to date"}) {
		t.Errorf("api log = %q", got)
	}
	if sess.notes["worker"] != nil {
		t.Error("nothing changed, but the other project in the repository got a line")
	}
}

func TestPullRefusedUpFront(t *testing.T) {
	m, _, f := newGitModel(t)
	for _, tt := range []struct {
		s    gitinfo.Status
		want string
	}{
		{gitinfo.Status{Branch: "main", Upstream: "origin/main", Changed: true}, "can't pull: there are uncommitted changes to tracked files"},
		{gitinfo.Status{Branch: "main", Upstream: "origin/main", Operation: gitinfo.Rebase}, "can't pull: a rebase is in progress"},
		{gitinfo.Status{Branch: "main"}, "can't pull: main has no upstream branch"},
		{gitinfo.Status{Commit: "0123456789", Detached: true}, "can't pull: HEAD is detached, not on a branch"},
	} {
		f.status["/r/app"] = tt.s
		press(t, m, "p")
		if m.confirm != nil || m.notice != tt.want {
			t.Errorf("%+v: confirmation %v, notice %q", tt.s, m.confirm != nil, m.notice)
		}
	}
	if len(f.pulls) != 0 {
		t.Error("pulled anyway")
	}
}

func TestPullCheckedAgainBeforePulling(t *testing.T) {
	m, sess, f := newGitModel(t)
	press(t, m, "p")
	f.status["/r/app"] = gitinfo.Status{Branch: "main", Upstream: "origin/main", Changed: true} // edited meanwhile
	press(t, m, "enter")
	if len(f.pulls) != 0 || len(sess.notes) != 0 {
		t.Errorf("pulled: %v, notes %v", f.pulls, sess.notes)
	}
	if m.notice != "can't pull: there are uncommitted changes to tracked files" || m.gitAction != "" {
		t.Errorf("notice %q, action %q", m.notice, m.gitAction)
	}
}

func TestPullFailure(t *testing.T) {
	m, sess, f := newGitModel(t)
	f.pullOut = "hint: Diverging branches can't be fast-forwarded, you need to either:\nfatal: Not possible to fast-forward, aborting.\n"
	f.pullErr = &gitinfo.Error{Args: gitinfo.PullArgs(), Stderr: f.pullOut, Err: errors.New("exit status 128")}
	press(t, m, "p")
	press(t, m, "enter")

	if want := "git pull: fatal: Not possible to fast-forward, aborting. (output in the log)"; m.notice != want {
		t.Errorf("notice = %q", m.notice)
	}
	if got := sess.notes["api"]; len(got) != 4 || got[0] != "$ git pull --ff-only --no-rebase" || got[3] != "git pull failed" {
		t.Errorf("log = %q", got)
	}
	if sess.notes["worker"] != nil {
		t.Error("a failed pull was noted for the other projects in the repository")
	}
	if !m.git.busy {
		t.Error("git state not refreshed after the failed pull")
	}
}

func TestPullConfirmationKeys(t *testing.T) {
	m, sess, f := newGitModel(t)
	for _, k := range []string{"esc", "n", "q"} {
		press(t, m, "p")
		press(t, m, "x") // ignored
		if m.confirm == nil {
			t.Fatalf("%s: confirmation closed by another key", k)
		}
		press(t, m, k)
		if m.confirm != nil || m.stopping {
			t.Errorf("%s: confirmation %v, stopping %v", k, m.confirm != nil, m.stopping)
		}
	}
	if len(f.pulls) != 0 {
		t.Error("pulled after cancelling")
	}

	m.gitAction = "git switch"
	press(t, m, "p")
	if m.confirm != nil || m.notice != "git switch is still running" {
		t.Errorf("busy: confirmation %v, notice %q", m.confirm != nil, m.notice)
	}
	m.gitAction = ""

	press(t, m, "p")
	m.Update(key("ctrl+c")) // its command waits for everything to stop
	if m.confirm != nil || !m.stopping || sess.stopAll != 1 {
		t.Errorf("ctrl+c: confirmation %v, stopping %v", m.confirm != nil, m.stopping)
	}
}

// newGitLauncher returns a launcher listing the group backend and the
// projects api, db and worker, with git state as in newGitModel.
func newGitLauncher(t *testing.T) (*launcherModel, *fakeGit) {
	t.Helper()
	cfg := testConfig()
	cfg.Groups = map[string][]string{"backend": {"db", "api"}}
	m := newLauncherModel(cfg, nil)
	f := testGit()
	f.status["/r/app"] = gitinfo.Status{Branch: "main", Commit: "0123456789", Upstream: "origin/main"}
	f.branches = map[string][]gitinfo.Branch{"/r/app": {
		{Name: "main", Current: true, Upstream: "origin/main", Date: "3 days ago"},
		{Name: "feat/login", Upstream: "origin/feat/login", Date: "2 hours ago"},
		{Name: "origin/fix/typo", Remote: true, Date: "5 days ago"},
	}}
	m.git = newGitTracker(f)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
	return m, f
}

// launcherPress sends a key to the launcher and runs the resulting git
// commands synchronously.
func launcherPress(t *testing.T, m *launcherModel, k string) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(key(k))
	return launcherFollow(t, m, cmd)
}

// launcherFollow runs cmd, feeding git action results back to m, and
// returns any other command it produced (e.g. startMsg, tea.Quit).
func launcherFollow(t *testing.T, m *launcherModel, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case branchesMsg, switchedMsg, pullCheckMsg, pulledMsg:
		_, next := m.Update(msg)
		return launcherFollow(t, m, next)
	case tea.BatchMsg:
		var rest tea.Cmd
		for _, c := range msg {
			if r := launcherFollow(t, m, c); r != nil {
				rest = r
			}
		}
		return rest
	case gitMsg, nil:
		return nil
	}
	return cmd
}

func launcherHelp(m *launcherModel) string {
	rows := launcherScreen(m)
	return rows[len(rows)-1]
}

func TestLauncherSwitchBranch(t *testing.T) {
	m, f := newGitLauncher(t)
	launcherPress(t, m, "b") // on the group
	if m.picker != nil || m.notice != "move to a project to switch its branch" {
		t.Errorf("group: picker %v, notice %q", m.picker != nil, m.notice)
	}
	if strings.Contains(launcherHelp(m), "b branch") {
		t.Error("b hint shown on a group")
	}

	launcherPress(t, m, "down") // api
	if got := launcherHelp(m); !strings.Contains(got, "d delete  b branch  p pull  q quit") {
		t.Errorf("help = %q", got)
	}
	launcherPress(t, m, "b")
	if m.picker == nil {
		t.Fatalf("no picker; notice %q", m.notice)
	}
	rows := launcherScreen(m)
	for _, want := range []string{
		" switch",
		" api is on main · worker shares the repository and switches too",
		" ▸ main        current · 3 days ago",
		"   fix/typo    new branch from origin/fix/typo · 5 days ago",
	} {
		if !slices.ContainsFunc(rows, func(r string) bool { return strings.HasPrefix(r, want) }) {
			t.Errorf("screen lacks %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if got := launcherHelp(m); got != " type to filter  ↑↓ select  enter switch  esc cancel" {
		t.Errorf("picker help = %q", got)
	}

	for _, k := range []string{"t", "y", "p", "o"} {
		launcherPress(t, m, k)
	}
	_, cmd := m.Update(key("enter"))
	if m.picker != nil || m.gitAction != "git switch" || m.notice != "switching api to fix/typo…" {
		t.Errorf("while switching: picker %v, action %q, notice %q", m.picker != nil, m.gitAction, m.notice)
	}
	if start := launcherPress(t, m, "enter"); start != nil || m.notice != "git switch is still running" {
		t.Errorf("start while switching: cmd %v, notice %q", start != nil, m.notice)
	}
	launcherFollow(t, m, cmd)

	if !slices.Equal(f.switches, []string{"/r/app origin/fix/typo"}) {
		t.Errorf("switches = %v", f.switches)
	}
	if m.notice != "switched api to fix/typo (also worker)" || m.gitAction != "" {
		t.Errorf("notice %q, action %q", m.notice, m.gitAction)
	}
	if !m.git.busy {
		t.Error("git state not refreshed after the switch")
	}
}

func TestLauncherSwitchBranchFailure(t *testing.T) {
	m, f := newGitLauncher(t)
	f.switchOut = "error: The following untracked working tree files would be overwritten by checkout:\n\tnew.txt\nAborting\n"
	f.switchErr = &gitinfo.Error{Args: []string{"switch", "feat/login"}, Stderr: f.switchOut, Err: errors.New("exit status 1")}
	launcherPress(t, m, "down")
	launcherPress(t, m, "b")
	launcherPress(t, m, "down")
	launcherPress(t, m, "enter")

	rows := launcherScreen(m)
	if !slices.Contains(rows, "  $ git switch feat/login") || !slices.Contains(rows, "  Aborting") || !slices.Contains(rows, "  press any key to close") {
		t.Errorf("output not shown:\n%s", strings.Join(rows, "\n"))
	}
	if want := " git switch: error: The following untracked working tree files would be overwritten by checkout:"; launcherHelp(m) != want {
		t.Errorf("help = %q", launcherHelp(m))
	}
	if cmd := launcherPress(t, m, "q"); cmd != nil || m.output != nil {
		t.Errorf("q: cmd %v, output %v", cmd != nil, m.output)
	}
	if !slices.ContainsFunc(launcherScreen(m), func(r string) bool { return strings.Contains(r, "Projects") }) {
		t.Error("list not shown again after closing the output")
	}
}

func TestLauncherSwitchBranchRefused(t *testing.T) {
	m, f := newGitLauncher(t)
	f.status["/r/app"] = gitinfo.Status{Branch: "main", Changed: true}
	launcherPress(t, m, "down")
	launcherPress(t, m, "b")
	if m.picker != nil || m.notice != "can't switch branches: there are uncommitted changes to tracked files" {
		t.Errorf("picker %v, notice %q", m.picker != nil, m.notice)
	}

	f.status["/r/app"] = gitinfo.Status{Branch: "main"}
	launcherPress(t, m, "b")
	if cmd := launcherPress(t, m, "esc"); m.picker != nil || cmd != nil {
		t.Errorf("esc: picker %v, cmd %v", m.picker != nil, cmd != nil)
	}
	if len(f.switches) != 0 {
		t.Error("switched anyway")
	}
}

func TestLauncherPull(t *testing.T) {
	m, f := newGitLauncher(t)
	launcherPress(t, m, "p") // on the group
	if m.ask != nil || m.notice != "move to a project to pull" {
		t.Errorf("group: question %v, notice %q", m.ask != nil, m.notice)
	}

	launcherPress(t, m, "down") // api
	launcherPress(t, m, "p")
	if m.ask == nil {
		t.Fatalf("no question; notice %q", m.notice)
	}
	if got, want := launcherHelp(m), " pull origin/main into api (main)? also updates worker (same repository)  enter pull  esc cancel"; got != want {
		t.Errorf("question = %q\nwant       %q", got, want)
	}
	f.pullOut = "Updating 0123456..fedcba9\nFast-forward\n"
	f.pulledTo = map[string]gitinfo.Status{"/r/app": {Branch: "main", Commit: "fedcba9876", Upstream: "origin/main"}}
	_, cmd := m.Update(key("enter"))
	if m.ask != nil || m.gitAction != "git pull" || m.notice != "pulling api…" {
		t.Errorf("while pulling: question %v, action %q, notice %q", m.ask != nil, m.gitAction, m.notice)
	}
	if start := launcherPress(t, m, "enter"); start != nil || m.notice != "git pull is still running" {
		t.Errorf("start while pulling: cmd %v, notice %q", start != nil, m.notice)
	}
	launcherFollow(t, m, cmd)
	if !slices.Equal(f.pulls, []string{"/r/app"}) {
		t.Errorf("pulls = %v", f.pulls)
	}
	if m.notice != "pulled api: main 0123456 → fedcba9 (also worker)" || m.gitAction != "" {
		t.Errorf("notice %q, action %q", m.notice, m.gitAction)
	}
	if !m.git.busy {
		t.Error("git state not refreshed after the pull")
	}

	launcherPress(t, m, "p")
	launcherPress(t, m, "y")
	if m.notice != "api is already up to date" {
		t.Errorf("up to date: notice %q", m.notice)
	}
}

func TestLauncherPullFailure(t *testing.T) {
	m, f := newGitLauncher(t)
	f.pullOut = "hint: Diverging branches can't be fast-forwarded, you need to either:\nfatal: Not possible to fast-forward, aborting.\n"
	f.pullErr = &gitinfo.Error{Args: gitinfo.PullArgs(), Stderr: f.pullOut, Err: errors.New("exit status 128")}
	launcherPress(t, m, "down")
	launcherPress(t, m, "p")
	launcherPress(t, m, "enter")

	rows := launcherScreen(m)
	if !slices.Contains(rows, "  $ git pull --ff-only --no-rebase") || !slices.Contains(rows, "  fatal: Not possible to fast-forward, aborting.") {
		t.Errorf("output not shown:\n%s", strings.Join(rows, "\n"))
	}
	if want := " git pull: fatal: Not possible to fast-forward, aborting."; launcherHelp(m) != want {
		t.Errorf("help = %q", launcherHelp(m))
	}
	launcherPress(t, m, "x")
	if m.output != nil {
		t.Error("output still shown after a key")
	}
}

func TestLauncherPullRefusedAndCancelled(t *testing.T) {
	m, f := newGitLauncher(t)
	launcherPress(t, m, "down")
	f.status["/r/app"] = gitinfo.Status{Branch: "main"}
	launcherPress(t, m, "p")
	if m.ask != nil || m.notice != "can't pull: main has no upstream branch" {
		t.Errorf("no upstream: question %v, notice %q", m.ask != nil, m.notice)
	}

	f.status["/r/app"] = gitinfo.Status{Branch: "main", Upstream: "origin/main"}
	for _, k := range []string{"esc", "n", "q"} {
		launcherPress(t, m, "p")
		launcherPress(t, m, "x") // ignored
		if m.ask == nil {
			t.Fatalf("%s: question closed by another key", k)
		}
		if cmd := launcherPress(t, m, k); m.ask != nil || cmd != nil {
			t.Errorf("%s: question %v, cmd %v", k, m.ask != nil, cmd != nil)
		}
	}
	if len(f.pulls) != 0 {
		t.Error("pulled after cancelling")
	}
}

func TestLauncherHelpKeepsQuitWhenNarrow(t *testing.T) {
	m, _ := newGitLauncher(t)
	launcherPress(t, m, "down")
	m.width = 60
	if got := launcherHelp(m); !strings.HasSuffix(got, "q quit") || ansi.StringWidth(got) > 60 {
		t.Errorf("help = %q", got)
	}
}

// TestBranchMessagesStayWithTheirModel checks that a reply for the launcher
// that arrives after the running view opened (or the other way round) is
// dropped.
func TestBranchMessagesStayWithTheirModel(t *testing.T) {
	l, _ := newGitLauncher(t)
	r, _, _ := newGitModel(t)
	branches := []gitinfo.Branch{{Name: "main", Current: true}}
	r.Update(branchesMsg{owner: l, project: "api", repo: gitinfo.Repo{Root: "/r/app"}, branches: branches})
	l.Update(branchesMsg{owner: r, project: "api", repo: gitinfo.Repo{Root: "/r/app"}, branches: branches})
	if r.picker != nil || l.picker != nil {
		t.Errorf("picker opened by another model's reply: running %v, launcher %v", r.picker != nil, l.picker != nil)
	}
	r.gitAction = "git switch"
	r.Update(switchedMsg{owner: l, project: "api", ran: true})
	if r.gitAction == "" {
		t.Error("running view handled the launcher's switch result")
	}
	r.Update(pullCheckMsg{owner: l, project: "api", status: gitinfo.Status{Branch: "main", Upstream: "origin/main"}})
	l.Update(pullCheckMsg{owner: r, project: "api", status: gitinfo.Status{Branch: "main", Upstream: "origin/main"}})
	if r.confirm != nil || l.ask != nil {
		t.Errorf("question opened by another model's reply: running %v, launcher %v", r.confirm != nil, l.ask != nil)
	}
	r.Update(pulledMsg{owner: l, project: "api", ran: true})
	if r.gitAction == "" {
		t.Error("running view handled the launcher's pull result")
	}
}

func TestOutputLines(t *testing.T) {
	got := outputLines("Updating files:  50% (1/2)\rUpdating files: 100% (2/2), done.\r\nSwitched to branch 'x'\n\n")
	want := []string{"Updating files: 100% (2/2), done.", "Switched to branch 'x'"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}
