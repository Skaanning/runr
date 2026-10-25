package tui

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/gitinfo"
	"github.com/Skaanning/runr/internal/ports"
	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/supervisor"
)

// fakeLookup returns the given listeners, filtered by pid like
// ports.Listeners, and records the pids it was asked about.
type fakeLookup struct {
	listeners []ports.Listener
	err       error
	asked     [][]int
}

func (f *fakeLookup) lookup(pids []int) ([]ports.Listener, error) {
	slices.Sort(pids)
	f.asked = append(f.asked, pids)
	var out []ports.Listener
	for _, l := range f.listeners {
		if slices.Contains(pids, l.Pid) {
			out = append(out, l)
		}
	}
	return out, f.err
}

// withPorts gives a test model's processes pids and the ports they listen
// on, and loads them.
func withPorts(t *testing.T, m *runningModel, sess *fakeSession, pids map[string][]int, ls ...ports.Listener) *fakeLookup {
	t.Helper()
	f := &fakeLookup{listeners: ls}
	sess.pids = pids
	m.ports.lookup = f.lookup
	m.ports.busy, m.ports.again = false, false // drop refreshes started by earlier events
	cmd := m.ports.refresh()
	if cmd == nil {
		t.Fatal("refresh returned no command")
	}
	m.Update(cmd())
	return f
}

func TestPortTracker(t *testing.T) {
	pids := map[string][]int{"api": {10, 11}, "web": {20}}
	f := &fakeLookup{listeners: []ports.Listener{
		{Pid: 11, Port: 9229}, {Pid: 10, Port: 8080}, {Pid: 11, Port: 8080}, {Pid: 20, Port: 5173}, {Pid: 99, Port: 22},
	}}
	tr := newPortTracker(func() map[string][]int { return pids })
	tr.lookup = f.lookup

	cmd := tr.refresh()
	if tr.refresh() != nil || !tr.again {
		t.Error("a second refresh while one runs should be queued")
	}
	msg := cmd()
	if !reflect.DeepEqual(f.asked, [][]int{{10, 11, 20}}) {
		t.Errorf("asked about %v", f.asked)
	}
	// The queued refresh runs right away instead of a tick.
	next := tr.update(msg, true)
	want := map[string][]int{"api": {8080, 9229}, "web": {5173}}
	if !reflect.DeepEqual(tr.state, want) {
		t.Errorf("state = %v, want %v", tr.state, want)
	}
	if next == nil {
		t.Fatal("queued refresh didn't run")
	}
	// While processes run, the next refresh is a tick away.
	tick := tr.update(next(), true)
	if tick == nil {
		t.Fatal("no tick scheduled")
	}
	if tr.update(portTickMsg{owner: tr, gen: tr.gen - 1}, true) != nil {
		t.Error("a stale tick started a refresh")
	}
	if tr.update(portTickMsg{owner: &portTracker{}, gen: tr.gen}, true) != nil {
		t.Error("another tracker's tick started a refresh")
	}
	cmd = tr.update(portTickMsg{owner: tr, gen: tr.gen}, true)
	if cmd == nil {
		t.Fatal("tick didn't refresh")
	}
	// Nothing runs: no tick.
	pids = map[string][]int{}
	msg = cmd()
	if tr.update(msg, false) != nil || len(tr.state) != 0 {
		t.Errorf("idle: state %v, want a stop", tr.state)
	}
	if len(f.asked) != 2 {
		t.Errorf("looked up with no pids: %v", f.asked) // nil would mean every process
	}

	// Ports that can't be read turn the tracker off.
	pids = map[string][]int{"api": {10}}
	f.err = ports.ErrUnsupported
	if tr.update(tr.refresh()(), true) != nil || !tr.off || tr.refresh() != nil {
		t.Error("tracker still refreshing without port support")
	}
	// Other errors keep it going.
	tr = newPortTracker(func() map[string][]int { return pids })
	tr.lookup = (&fakeLookup{err: errors.New("boom")}).lookup
	if tr.update(tr.refresh()(), true) == nil || tr.off {
		t.Error("a failed lookup stopped the refreshes")
	}
}

func TestPortsLabel(t *testing.T) {
	for want, ps := range map[string][]int{"": nil, ":8080": {8080}, ":5173 +1": {5173, 24678}, ":3000 +2": {3000, 3001, 9229}} {
		if got := portsLabel(ps); got != want {
			t.Errorf("portsLabel(%v) = %q, want %q", ps, got, want)
		}
	}
}

func TestRunningSidebarPorts(t *testing.T) {
	for _, tt := range []struct {
		width int
		want  []string // the sidebar, trimmed
	}{
		{100, []string{ // the sidebar grows to fit the second lines
			" ▌● db                ▁▁▁▁▁▁▁▁",
			" ▌  :5432 · main",
			"  ● api               ▁▁▁▁▁▁▁▂",
			"    :8080 +1 · feat/login * ↓2",
			"  ✗ Worker     exit 1 ▁▁▁▁▁▁▁▁",
			"    feat/login * ↓2", // ports only while it runs
		}},
		{52, []string{ // 20 columns: the git label is cut, the ports stay
			" ▌● db",
			" ▌  :5432 · main",
			"  ● api",
			"    :8080 +1 · fea…",
			"  ✗ Worker   exit 1",
			"    feature/l… * ↓2",
		}},
	} {
		m, sess := newTestModel(t, tt.width, 12)
		g := testGit()
		if tt.width < 100 {
			g.status["/r/app"] = gitinfo.Status{Branch: "feature/long-branch-name", Changed: true, Behind: 2}
		}
		m.git = newGitTracker(g)
		loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
		m.Update(eventsMsg{events: []supervisor.Event{
			state("db", supervisor.Running), state("api", supervisor.Running), line("api", "hello"),
			supervisor.StateEvent{Info: supervisor.ProcessInfo{Name: "worker", State: supervisor.Crashed, Exit: proc.ExitStatus{Code: 1}}},
		}})
		withPorts(t, m, sess, map[string][]int{"db": {1}, "api": {2, 3}, "worker": {4}},
			ports.Listener{Pid: 1, Port: 5432}, ports.Listener{Pid: 2, Port: 8080}, ports.Listener{Pid: 3, Port: 9229},
			ports.Listener{Pid: 4, Port: 7000})
		if got := sidebarText(m)[:6]; !slices.Equal(got, tt.want) {
			t.Errorf("width %d:\n%s\nwant:\n%s", tt.width, strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
		}
		for i, row := range strings.Split(m.render(), "\n") {
			if w := ansi.StringWidth(row); w > tt.width {
				t.Errorf("width %d: row %d is %d cells", tt.width, i, w)
			}
		}
	}
}

func TestRunningPortsWithoutGit(t *testing.T) {
	m, sess := newTestModel(t, 50, 12)
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running)}})
	withPorts(t, m, sess, map[string][]int{"db": {1}}, ports.Listener{Pid: 1, Port: 54321}, ports.Listener{Pid: 1, Port: 54322})
	if got := sidebarText(m)[1]; got != " ▌  :54321 +1" {
		t.Errorf("details = %q", got)
	}
	// The compact layout has no second line.
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if got := sidebarText(m); strings.Contains(strings.Join(got, "\n"), ":54321") {
		t.Errorf("compact sidebar shows ports:\n%s", strings.Join(got, "\n"))
	}
}

func TestRunningRefreshesPortsOnStateChange(t *testing.T) {
	m, sess := newTestModel(t, 100, 12)
	f := withPorts(t, m, sess, map[string][]int{})
	sess.pids = map[string][]int{"db": {1}}
	f.listeners = []ports.Listener{{Pid: 1, Port: 5432}}
	cmd := m.handleEvents([]supervisor.Event{state("db", supervisor.Running)})
	if cmd == nil {
		t.Fatal("no refresh after a state change")
	}
	m.Update(cmd())
	if got := m.ports.get("db"); !slices.Equal(got, []int{5432}) {
		t.Errorf("db ports = %v", got)
	}
	// Log lines alone don't refresh.
	if m.handleEvents([]supervisor.Event{line("db", "x")}) != nil {
		t.Error("refresh after a log line")
	}
}

// fakeBrowser replaces openURL for a test and records the URLs opened.
func fakeBrowser(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	old := openURL
	openURL = func(url string) error { opened = append(opened, url); return err }
	t.Cleanup(func() { openURL = old })
	return &opened
}

// pressOpen presses o and handles the resulting messages.
func pressOpen(t *testing.T, m *runningModel, k string) {
	t.Helper()
	_, cmd := m.Update(key(k))
	if cmd == nil {
		return
	}
	if msg, ok := cmd().(openedMsg); ok {
		m.Update(msg)
	}
}

func TestOpenPort(t *testing.T) {
	opened := fakeBrowser(t, nil)
	m, sess := newTestModel(t, 100, 14)
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running), state("api", supervisor.Running)}})
	withPorts(t, m, sess, map[string][]int{"db": {1}, "api": {2}},
		ports.Listener{Pid: 1, Port: 5432}, ports.Listener{Pid: 2, Port: 8080}, ports.Listener{Pid: 2, Port: 9229})

	// One port: opened right away.
	if got := helpRow(m); !strings.Contains(got, "o  open") {
		t.Errorf("help = %q", got)
	}
	pressOpen(t, m, "o")
	if !slices.Equal(*opened, []string{"http://localhost:5432"}) || m.notice != "opened http://localhost:5432" {
		t.Fatalf("opened %q, notice %q", *opened, m.notice)
	}

	// Several: a picker first.
	m.Update(key("down"))
	pressOpen(t, m, "o")
	if m.picker == nil {
		t.Fatalf("no picker; notice %q", m.notice)
	}
	screenHas(t, screen(m), "─ open · api ─", " 2 ports ", ":8080", ":9229", "http://localhost:9229")
	m.Update(key("down"))
	pressOpen(t, m, "enter")
	if m.picker != nil || !slices.Equal(*opened, []string{"http://localhost:5432", "http://localhost:9229"}) {
		t.Errorf("picker %v, opened %q", m.picker != nil, *opened)
	}

	// No ports: a notice.
	m.Update(key("down"))
	pressOpen(t, m, "o")
	if m.notice != "Worker isn't listening on a port" || len(*opened) != 2 {
		t.Errorf("notice %q, opened %q", m.notice, *opened)
	}
	if got := helpRow(m); strings.Contains(got, "o  open") {
		t.Errorf("help offers open without ports: %q", got)
	}
}

func TestOpenPortFails(t *testing.T) {
	fakeBrowser(t, errors.New(`exec: "xdg-open": executable file not found in $PATH`))
	m, sess := newTestModel(t, 100, 14)
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running)}})
	withPorts(t, m, sess, map[string][]int{"db": {1}}, ports.Listener{Pid: 1, Port: 5432})
	pressOpen(t, m, "o")
	if want := `can't open http://localhost:5432: exec: "xdg-open": executable file not found in $PATH`; m.notice != want {
		t.Errorf("notice = %q", m.notice)
	}
	// Ports of a process that ended aren't opened.
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Stopped)}})
	pressOpen(t, m, "o")
	if m.notice != "db isn't listening on a port" {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestRunningRemembersPorts(t *testing.T) {
	m, sess := newTestModel(t, 100, 12) // nothing running: no tick to wait for
	sess.pids = map[string][]int{"db": {1}}
	m.ports.busy = false
	m.ports.lookup = (&fakeLookup{listeners: []ports.Listener{{Pid: 1, Port: 5432}}}).lookup
	_, cmd := m.Update(m.ports.refresh()())
	runAll(cmd)
	if want := map[string][]int{"db": {5432}}; !reflect.DeepEqual(sess.seen, want) {
		t.Errorf("remembered %v, want %v", sess.seen, want)
	}
}

// runAll runs cmd and the commands it batches, discarding their messages.
func runAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runAll(c)
		}
	}
}

func TestWarningToast(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{
		state("api", supervisor.Running),
		supervisor.WarningEvent{Project: "api", Text: "port 8080 is in use by pid 1234 (node)"},
	}})
	want := []string{
		"╭──────────────────────────────────────────────╮",
		"│ ! api port 8080 is in use by pid 1234 (node) │",
		"│   ! select it                                │",
		"╰──────────────────────────────────────────────╯",
	}
	if got := toastText(m); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("toast:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(m.render(), m.st.warn.Render(want[0])) {
		t.Error("a warning's border should be yellow")
	}
	m.Update(key("!"))
	m.Update(supervisor.WarningEvent{}) // not a message the model handles
	m.Update(eventsMsg{events: []supervisor.Event{supervisor.WarningEvent{Project: "api", Text: "port 8080 is in use"}}})
	if got := toastText(m); len(got) != 4 || !strings.Contains(got[2], "tab see its log") {
		t.Errorf("toast for the selected project:\n%s", strings.Join(got, "\n"))
	}
}

func TestWarningToastKeptOnCrash(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{
		state("api", supervisor.Running),
		supervisor.WarningEvent{Project: "api", Text: "port 8080 is in use"},
		crash("api", 1),
	}})
	got := strings.Join(toastText(m), "\n")
	if !strings.Contains(got, "! api port 8080 is in use") || !strings.Contains(got, "✗ api crashed · exit code 1") {
		t.Errorf("toasts:\n%s", got)
	}
}
