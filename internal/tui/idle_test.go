package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/supervisor"
)

// newIdleModel has db and api running, and cron, docs (missing dir) and
// worker (needs db) registered but not started.
func newIdleModel(t *testing.T, width, height int) (*runningModel, *fakeSession) {
	t.Helper()
	sess := newFakeSession("db", "api")
	m := newRunningModel(sess, pickerConfig(t), []string{"backend"}, newStyles(terminalTheme))
	m.now = testNow
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running), state("api", supervisor.Running)}})
	return m, sess
}

// sidebarText returns the sidebar's rows, trimmed.
func sidebarText(m *runningModel) []string {
	rows := screen(m)
	body, divider := rows[2:len(rows)-2], "│"
	if m.panes() {
		body, divider = rows[1:len(rows)-3], "││"
	}
	var out []string
	for _, r := range body {
		side, _, _ := strings.Cut(r, divider)
		out = append(out, strings.TrimRight(strings.TrimPrefix(side, "│"), " "))
	}
	return out
}

func checkSidebar(t *testing.T, m *runningModel, want ...string) {
	t.Helper()
	got := sidebarText(m)
	for len(want) < len(got) {
		want = append(want, "")
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sidebar:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestIdleProjectsListed(t *testing.T) {
	m, _ := newIdleModel(t, 80, 12)
	checkSidebar(t, m,
		" ▌● db  ▁▁▁▁▁▁▁▁",
		"  ● api ▁▁▁▁▁▁▁▁",
		" not running",
		"  ○ cron",
		"  ○ docs",
		"  ○ Worker",
	)
	raw := strings.Split(m.render(), "\n")
	if !strings.Contains(raw[4], m.st.faint.Render("cron")) || !strings.Contains(raw[4], m.st.faint.Render("○")) {
		t.Errorf("not-started row isn't faint: %q", raw[4])
	}

	// Nothing to list when every project is in the session.
	m2, _ := newTestModel(t, 80, 12)
	for _, r := range screen(m2) {
		if strings.Contains(r, "not running") {
			t.Fatalf("heading shown with every project in the session: %q", r)
		}
	}
}

func TestIdleSetting(t *testing.T) {
	m, _ := newPickerModel(t) // show_all = false
	if len(m.idle) != 0 || strings.Contains(strings.Join(screen(m), "\n"), "not running") {
		t.Fatal("projects not started are listed with show_all = false")
	}
	if got := helpRow(m); !strings.Contains(got, "+  start") {
		t.Errorf("help = %q, want + start", got)
	}
}

func TestIdleSelectAndStart(t *testing.T) {
	m, sess := newIdleModel(t, 80, 12)
	m.Update(key("down"))
	m.Update(key("down")) // cron: the heading is skipped
	if got := m.selectedIdle(); got != "cron" {
		t.Fatalf("selected %q, want cron", got)
	}
	help := helpRow(m)
	if !strings.Contains(help, "s  start") || strings.Contains(help, "restart") {
		t.Errorf("help for a project not started = %q", help)
	}
	if got := statusRow(m); !strings.Contains(got, "cron  14:32") {
		t.Errorf("status bar = %q, want the selected project", got)
	}

	m.Update(key("s"))
	if got := strings.Join(sess.calls, ","); got != "start cron" {
		t.Fatalf("calls = %q", got)
	}
	checkSidebar(t, m,
		"  ● db   ▁▁▁▁▁▁▁▁",
		"  ● api  ▁▁▁▁▁▁▁▁",
		" ▌○ cron ▁▁▁▁▁▁▁▁",
		" not running",
		"  ○ docs",
		"  ○ Worker",
	)
	if got := statusRow(m); !strings.HasPrefix(got, "  FOLLOW  backend + cron ") {
		t.Errorf("status bar = %q, want cron in the title", got)
	}

	// enter starts too; on a project in the session it does nothing.
	m.Update(key("down"))
	m.Update(key("down")) // Worker
	m.Update(key("enter"))
	m.Update(key("up"))
	m.Update(key("enter"))
	if got := strings.Join(sess.calls, ","); got != "start cron,start worker" {
		t.Fatalf("calls = %q", got)
	}
	if m.selectedName() != "cron" {
		t.Errorf("selected %q after up from worker", m.selectedName())
	}
}

func TestIdleStartWithDependencies(t *testing.T) {
	sess := newFakeSession("cron")
	m := newRunningModel(sess, pickerConfig(t), []string{"cron"}, newStyles(terminalTheme))
	m.now = testNow
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m.selectEntry(m.indexOf("api"))
	m.Update(key("r")) // restarting what isn't running starts it
	if got := strings.Join(sess.calls, ","); got != "start api" {
		t.Fatalf("calls = %q", got)
	}
	checkSidebar(t, m,
		"  ○ cron ▁▁▁▁▁▁▁▁",
		"  ○ db   ▁▁▁▁▁▁▁▁",
		" ▌○ api  ▁▁▁▁▁▁▁▁",
		" not running",
		"  ○ docs",
		"  ○ Worker",
	)
}

func TestIdleMissingDir(t *testing.T) {
	m, sess := newIdleModel(t, 80, 12)
	m.selectEntry(m.indexOf("docs"))
	m.Update(key("s"))
	if len(sess.calls) != 0 {
		t.Fatalf("calls = %q", sess.calls)
	}
	if got := helpRow(m); !strings.HasPrefix(got, ` cannot start: project "docs": dir: `) {
		t.Errorf("help = %q", got)
	}
	if m.byName["docs"] != nil || m.selectedIdle() != "docs" {
		t.Error("docs moved into the session although it didn't start")
	}
}

func TestIdleSingleView(t *testing.T) {
	m, sess := newIdleModel(t, 80, 12)
	m.selectEntry(m.indexOf("cron"))
	m.Update(key("tab"))
	if m.singleFor != "cron" || len(logRows(m)) != 0 {
		t.Fatalf("single view of %q with %v", m.singleFor, logRows(m))
	}
	if top := screen(m)[0]; !strings.Contains(top, "logs · cron") {
		t.Errorf("pane title = %q", top)
	}
	if m.byName["cron"] != nil {
		t.Fatal("showing the log added cron to the session")
	}
	m.Update(key("s"))
	m.Update(eventsMsg{events: []supervisor.Event{emit(sess, "cron", "tick")}})
	if got := logRows(m); !slices.Equal(got, []string{"tick"}) {
		t.Errorf("log after starting = %v", got)
	}
	m.Update(key("down")) // the single view follows the selection to docs
	if m.singleFor != "docs" {
		t.Errorf("single view shows %q, want docs", m.singleFor)
	}
}

func TestIdleToggle(t *testing.T) {
	m, _ := newIdleModel(t, 160, 12)
	m.selectEntry(m.indexOf("worker"))
	if got := helpRow(m); !strings.Contains(got, "a  hide others") {
		t.Errorf("help = %q", got)
	}
	m.Update(key("a"))
	checkSidebar(t, m, "  ● db  ▁▁▁▁▁▁▁▁", " ▌● api ▁▁▁▁▁▁▁▁")
	if got := helpRow(m); !strings.Contains(got, "a  show all") || !strings.Contains(got, "+  start") {
		t.Errorf("help = %q", got)
	}
	m.Update(key("+")) // lists projects again
	if got := logRows(m); !slices.ContainsFunc(got, func(r string) bool { return strings.HasSuffix(r, " cron") }) {
		t.Errorf("picker without the projects not started: %v", got)
	}
	m.Update(key("esc"))
	m.Update(key("a"))
	if len(m.idle) != 3 || m.selectedName() != "api" {
		t.Errorf("after showing again: idle %v, selected %q", m.idle, m.selectedName())
	}
	m.Update(key("+")) // groups only
	if got := strings.Join(logRows(m), ","); strings.Contains(got, "cron") || !strings.Contains(got, "Groups") {
		t.Errorf("picker with every project in the sidebar: %v", got)
	}
}

func TestIdleShortTerminal(t *testing.T) {
	m, _ := newIdleModel(t, 60, 8) // 4 rows, no panes
	checkSidebar(t, m, " ▌ ● db  ▁▁▁▁▁▁▁▁", "   ● api ▁▁▁▁▁▁▁▁", " not running", "   ○ cron")
	m.selectEntry(m.indexOf("worker"))
	checkSidebar(t, m, "   ● db  ▁▁▁▁▁▁▁▁", "   ● api ▁▁▁▁▁▁▁▁", " not running", " ▌ ○ Worker")

	// With no room left, the session's projects keep the sidebar, until one
	// not started is selected.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 6}) // 2 rows
	m.selectEntry(m.indexOf("api"))
	checkSidebar(t, m, "   ● db  ▁▁▁▁▁▁▁▁", " ▌ ● api ▁▁▁▁▁▁▁▁")
	m.Update(key("down"))
	checkSidebar(t, m, " not running", " ▌ ○ cron")
	m.Update(key("up"))
	checkSidebar(t, m, "   ● db  ▁▁▁▁▁▁▁▁", " ▌ ● api ▁▁▁▁▁▁▁▁")
}

func TestIdleGit(t *testing.T) {
	m, _ := newIdleModel(t, 100, 12)
	if dirs := m.gitDirs(); len(dirs) != 5 {
		t.Errorf("git dirs = %v, want the projects not started too", dirs)
	}
	f := testGit()
	m.git = newGitTracker(f)
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
	rows := sidebarText(m)
	if !strings.HasPrefix(rows[5], "  ○ Worker ") || !strings.HasSuffix(rows[5], " feat/login * ↓2") {
		t.Errorf("worker row = %q, want its git label", rows[5])
	}
	m.selectEntry(m.indexOf("worker"))
	if got := helpRow(m); !strings.Contains(got, "b  branch") {
		t.Errorf("help = %q, want b for a project not started", got)
	}
	m.Update(key("b"))
	if m.byName["worker"] != nil || m.notice != "" {
		t.Errorf("b added worker to the session (notice %q)", m.notice)
	}
}
