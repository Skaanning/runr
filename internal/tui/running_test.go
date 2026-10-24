package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"runr/internal/config"
	"runr/internal/proc"
	"runr/internal/supervisor"
)

type fakeSession struct {
	procs    []supervisor.ProcessInfo
	events   chan supervisor.Event
	stopDone chan struct{}
	stopAll  int
	killAll  int
}

func newFakeSession(names ...string) *fakeSession {
	f := &fakeSession{events: make(chan supervisor.Event, maxBatch+1000), stopDone: make(chan struct{})}
	for _, n := range names {
		f.procs = append(f.procs, supervisor.ProcessInfo{Name: n})
	}
	return f
}

func (f *fakeSession) Processes() []supervisor.ProcessInfo { return f.procs }
func (f *fakeSession) Events() <-chan supervisor.Event     { return f.events }
func (f *fakeSession) StopAll() <-chan struct{}            { f.stopAll++; return f.stopDone }
func (f *fakeSession) KillAll()                            { f.killAll++ }

func testConfig() *config.Config {
	return &config.Config{Projects: map[string]*config.Project{
		"db":     {Cmd: "db"},
		"api":    {Cmd: "api", Color: "cyan"},
		"worker": {Cmd: "worker", DisplayName: "Worker"},
	}}
}

func newTestModel(t *testing.T, width, height int) (*runningModel, *fakeSession) {
	t.Helper()
	sess := newFakeSession("db", "api", "worker")
	m := newRunningModel(sess, testConfig(), "backend")
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m, sess
}

func state(name string, s supervisor.State) supervisor.Event {
	return supervisor.StateEvent{Info: supervisor.ProcessInfo{Name: name, State: s}}
}

func line(name, text string) supervisor.Event {
	return supervisor.LineEvent{Line: supervisor.LogLine{Project: name, Text: text}}
}

func system(name, text string) supervisor.Event {
	return supervisor.LineEvent{Line: supervisor.LogLine{Project: name, Stream: supervisor.System, Text: text}}
}

// screen returns the rendered view without styling, one string per row,
// with trailing spaces removed.
func screen(m *runningModel) []string {
	rows := strings.Split(ansi.Strip(m.render()), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return rows
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func TestRunningLayout(t *testing.T) {
	m, _ := newTestModel(t, 60, 8)
	crashed := supervisor.ProcessInfo{Name: "worker", State: supervisor.Crashed, Exit: proc.ExitStatus{Code: 1}}
	m.Update(eventsMsg{events: []supervisor.Event{
		state("db", supervisor.Running),
		state("api", supervisor.Starting),
		supervisor.StateEvent{Info: crashed},
		system("db", "starting: db"),
		line("api", "listening on :8080"),
		line("worker", "\x1b[31merror\x1b[0m: queue not found\x1b[2K"),
	}})

	want := []string{
		" runr · backend                        combined · following",
		"────────────────┬───────────────────────────────────────────",
		" ▸ ● db         │ db     starting: db",
		"   ◐ api        │ api    listening on :8080",
		"   ✗ Worker 1   │ Worker error: queue not found",
		"                │",
		"────────────────┴───────────────────────────────────────────",
		" ↑↓ select  pgup/pgdn scroll  g/G top/bottom  q quit",
	}
	got := screen(m)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("screen:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i, row := range strings.Split(m.render(), "\n") {
		if w := ansi.StringWidth(row); w > 60 {
			t.Errorf("row %d is %d cells wide", i, w)
		}
	}
}

func TestRunningSelectionAndSidebarScroll(t *testing.T) {
	sess := newFakeSession()
	cfg := &config.Config{Projects: map[string]*config.Project{}}
	for i := range 10 {
		name := fmt.Sprintf("p%d", i)
		cfg.Projects[name] = &config.Project{Cmd: "x"}
		sess.procs = append(sess.procs, supervisor.ProcessInfo{Name: name})
	}
	m := newRunningModel(sess, cfg, "all")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 7}) // 3 body rows

	m.Update(key("up")) // stays at the first entry
	if m.selected != 0 {
		t.Fatalf("selected = %d", m.selected)
	}
	for range 4 {
		m.Update(key("down"))
	}
	if got := screen(m)[2:5]; !strings.HasPrefix(got[0], "   ○ p2") || !strings.HasPrefix(got[2], " ▸ ○ p4") {
		t.Fatalf("sidebar = %q", got)
	}
	for range 20 {
		m.Update(key("down"))
	}
	if m.selected != 9 || m.sideTop != 7 {
		t.Fatalf("selected = %d, sideTop = %d", m.selected, m.sideTop)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 20}) // everything fits again
	if m.sideTop != 0 {
		t.Fatalf("sideTop = %d after growing", m.sideTop)
	}
}

func TestRunningScrollKeys(t *testing.T) {
	m, _ := newTestModel(t, 60, 6) // 2 body rows
	var events []supervisor.Event
	for i := range 10 {
		events = append(events, line("db", fmt.Sprint("line ", i)))
	}
	m.Update(eventsMsg{events: events})

	m.Update(key("pgup"))
	got := screen(m)
	if !strings.HasSuffix(got[0], "combined · paused · 1 more below") {
		t.Fatalf("header = %q", got[0])
	}
	if !strings.HasSuffix(got[3], "line 8") {
		t.Fatalf("bottom log row = %q", got[3])
	}
	m.Update(key("G"))
	if !strings.HasSuffix(screen(m)[0], "following") {
		t.Fatal("G did not resume following")
	}
	m.Update(key("g"))
	if !strings.HasSuffix(screen(m)[2], "line 0") {
		t.Fatalf("g: first log row = %q", screen(m)[2])
	}
	m.Update(key("f"))
	if !strings.HasSuffix(screen(m)[0], "following") {
		t.Fatal("f did not resume following")
	}
	m.Update(key("f"))
	if !strings.Contains(screen(m)[0], "paused") {
		t.Fatal("f did not pause")
	}
}

func TestRunningWrapsLongLinesUnderLabel(t *testing.T) {
	m, _ := newTestModel(t, 40, 6)
	// sidebar 40/3 = 13, log column 40-13-2 = 25 cells, label column 7 → 18 cells of text
	m.Update(eventsMsg{events: []supervisor.Event{line("api", "abcdefghijklmnopqrstuvwxyz")}})
	got := screen(m)
	for i, want := range []string{"│ api    abcdefghijklmnopqr", "│        stuvwxyz"} {
		if !strings.HasSuffix(got[2+i], want) {
			t.Errorf("row %d = %q, want suffix %q", 2+i, got[2+i], want)
		}
	}
}

func TestRunningNewProjectFromEvents(t *testing.T) {
	m, _ := newTestModel(t, 60, 10)
	m.cfg.Projects["frontend-application"] = &config.Project{Cmd: "x"}
	m.Update(eventsMsg{events: []supervisor.Event{
		state("frontend-application", supervisor.Waiting),
		line("frontend-application", "hi"),
	}})
	got := screen(m)
	if !strings.HasSuffix(got[5], "◐ frontend-appl… │") {
		t.Fatalf("sidebar row = %q", got[5])
	}
	if !strings.HasSuffix(got[2], "│ frontend-applic… hi") {
		t.Fatalf("log row = %q", got[2])
	}
}

func TestRunningQuit(t *testing.T) {
	m, sess := newTestModel(t, 60, 8)
	_, cmd := m.Update(key("q"))
	if sess.stopAll != 1 || cmd == nil {
		t.Fatalf("stopAll = %d, cmd = %v", sess.stopAll, cmd)
	}
	if !strings.Contains(screen(m)[0], "stopping… ctrl+c to force") {
		t.Fatalf("header = %q", screen(m)[0])
	}
	m.Update(key("q")) // ignored while stopping
	if sess.stopAll != 1 || sess.killAll != 0 {
		t.Fatalf("second q: stopAll = %d, killAll = %d", sess.stopAll, sess.killAll)
	}
	m.Update(key("ctrl+c"))
	m.Update(interruptMsg{})
	if sess.killAll != 1 {
		t.Fatalf("killAll = %d after forcing", sess.killAll)
	}

	result := make(chan tea.Msg)
	go func() { result <- cmd() }()
	select {
	case <-result:
		t.Fatal("quit before StopAll finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(sess.stopDone)
	msg := <-result
	if _, ok := msg.(stoppedMsg); !ok {
		t.Fatalf("msg = %#v", msg)
	}
	if _, cmd := m.Update(msg); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("stoppedMsg did not quit")
	}
}

func TestRunningInterruptStartsShutdown(t *testing.T) {
	m, sess := newTestModel(t, 60, 8)
	m.Update(interruptMsg{})
	if sess.stopAll != 1 {
		t.Fatalf("stopAll = %d", sess.stopAll)
	}
}

func TestWaitEventsBatches(t *testing.T) {
	m, sess := newTestModel(t, 60, 8)
	for i := range maxBatch + 1000 {
		sess.events <- line("db", fmt.Sprint(i))
	}
	msg := m.waitEvents()().(eventsMsg)
	if len(msg.events) != maxBatch || msg.closed {
		t.Fatalf("batch of %d, closed = %v", len(msg.events), msg.closed)
	}
	m.Update(msg)
	msg = m.waitEvents()().(eventsMsg)
	if len(msg.events) != 1000 {
		t.Fatalf("second batch of %d", len(msg.events))
	}

	start := time.Now()
	m.lastBatch = start
	sess.events <- line("db", "x")
	close(sess.events)
	msg = m.waitEvents()().(eventsMsg)
	if len(msg.events) != 1 || !msg.closed {
		t.Fatalf("batch of %d, closed = %v", len(msg.events), msg.closed)
	}
	if time.Since(start) < frameInterval {
		t.Fatal("batch delivered faster than the frame interval")
	}
	if msg := m.waitEvents()().(eventsMsg); !msg.closed || len(msg.events) != 0 {
		t.Fatalf("after close: %#v", msg)
	}
}

func TestRunningTooSmall(t *testing.T) {
	m, _ := newTestModel(t, 20, 4)
	if got := m.render(); got != "terminal too small" {
		t.Fatalf("render = %q", got)
	}
}
