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
	logs     *supervisor.Logs
	stopDone chan struct{}
	stopAll  int
	killAll  int
	calls    []string // Start/Stop/Restart calls, e.g. "stop api"
	err      error    // returned by Start/Stop/Restart
}

func newFakeSession(names ...string) *fakeSession {
	f := &fakeSession{
		events:   make(chan supervisor.Event, maxBatch+1000),
		logs:     supervisor.NewLogs(100),
		stopDone: make(chan struct{}),
	}
	for _, n := range names {
		f.procs = append(f.procs, supervisor.ProcessInfo{Name: n})
	}
	return f
}

func (f *fakeSession) Processes() []supervisor.ProcessInfo { return f.procs }
func (f *fakeSession) Logs() *supervisor.Logs              { return f.logs }
func (f *fakeSession) Start(names ...string) error {
	f.calls = append(f.calls, "start "+strings.Join(names, " "))
	return f.err
}
func (f *fakeSession) Stop(name string) error { f.calls = append(f.calls, "stop "+name); return f.err }
func (f *fakeSession) Restart(name string) error {
	f.calls = append(f.calls, "restart "+name)
	return f.err
}
func (f *fakeSession) Events() <-chan supervisor.Event { return f.events }
func (f *fakeSession) StopAll() <-chan struct{}        { f.stopAll++; return f.stopDone }
func (f *fakeSession) KillAll()                        { f.killAll++ }

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
		" ↑↓ select  tab view  r restart  s stop  q quit",
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
	if !strings.Contains(screen(m)[0], "stopping… 0 left · ctrl+c to force") {
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

// emit appends a line to the session's logs and returns its event, like the
// supervisor does.
func emit(sess *fakeSession, project, text string) supervisor.Event {
	return supervisor.LineEvent{Line: sess.logs.Append(project, supervisor.Stdout, text)}
}

// logRows returns the log column of the body rows, trimmed.
func logRows(m *runningModel) []string {
	var out []string
	rows := screen(m)
	for _, r := range rows[2 : len(rows)-2] {
		_, text, _ := strings.Cut(r, "│")
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func TestRunningSingleView(t *testing.T) {
	m, sess := newTestModel(t, 60, 10)
	m.Update(eventsMsg{events: []supervisor.Event{
		emit(sess, "db", "a"), emit(sess, "api", "b"), emit(sess, "db", "c"),
	}})
	pending := emit(sess, "db", "c2") // in the buffer, event not delivered yet

	m.Update(key("tab")) // db is selected
	if got := strings.Join(logRows(m), ","); got != "a,c,c2" {
		t.Fatalf("single view = %q", got)
	}
	if got := screen(m)[0]; !strings.HasSuffix(got, "db · following") {
		t.Fatalf("header = %q", got)
	}
	if got := screen(m)[len(screen(m))-1]; !strings.Contains(got, "tab all") {
		t.Fatalf("help = %q", got)
	}

	m.Update(eventsMsg{events: []supervisor.Event{pending, emit(sess, "api", "x"), emit(sess, "db", "d")}})
	if got := strings.Join(logRows(m), ","); got != "a,c,c2,d" {
		t.Fatalf("after events = %q", got)
	}

	m.Update(key("down"))
	if got := strings.Join(logRows(m), ","); got != "b,x" {
		t.Fatalf("api view = %q", got)
	}
	if got := screen(m)[0]; !strings.HasSuffix(got, "api · following") {
		t.Fatalf("header = %q", got)
	}

	m.Update(key("tab"))
	want := "db     a,api    b,db     c,db     c2,api    x,db     d"
	if got := strings.Join(logRows(m), ","); got != want {
		t.Fatalf("combined = %q, want %q", got, want)
	}
}

func TestRunningCombinedKeepsScrollAcrossViews(t *testing.T) {
	m, sess := newTestModel(t, 60, 6)
	var events []supervisor.Event
	for i := range 10 {
		events = append(events, emit(sess, "db", fmt.Sprint("line ", i)))
	}
	m.Update(eventsMsg{events: events})
	m.Update(key("g"))
	m.Update(key("tab"))
	if !strings.HasSuffix(screen(m)[0], "db · following") {
		t.Fatalf("single header = %q", screen(m)[0])
	}
	m.Update(key("tab"))
	if got := screen(m)[0]; !strings.Contains(got, "combined · paused") {
		t.Fatalf("combined header = %q", got)
	}
	if got := logRows(m)[0]; got != "db     line 0" {
		t.Fatalf("first row = %q", got)
	}
}

func TestRunningStopStartRestart(t *testing.T) {
	m, sess := newTestModel(t, 80, 10)
	setState := func(name string, s supervisor.State) {
		m.Update(eventsMsg{events: []supervisor.Event{state(name, s)}})
	}
	helpRow := func() string { rows := screen(m); return rows[len(rows)-1] }

	setState("db", supervisor.Running)
	setState("api", supervisor.Waiting)
	setState("worker", supervisor.Crashed)

	m.Update(key("s")) // db running → stop
	m.Update(key("down"))
	m.Update(key("s")) // api waiting → stop (cancel)
	m.Update(key("down"))
	if !strings.Contains(helpRow(), "s start") {
		t.Fatalf("help = %q", helpRow())
	}
	m.Update(key("s")) // worker crashed → start
	m.Update(key("r"))
	setState("worker", supervisor.Stopping)
	m.Update(key("s")) // already stopping: nothing

	want := "stop db,stop api,start worker,restart worker"
	if got := strings.Join(sess.calls, ","); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}

	sess.err = fmt.Errorf("shutting down")
	m.Update(key("r"))
	if got := helpRow(); got != " shutting down" {
		t.Fatalf("help = %q", got)
	}
	m.Update(key("down"))
	if strings.Contains(helpRow(), "shutting down") {
		t.Fatal("notice not cleared by the next key")
	}

	sess.calls = nil
	m.Update(key("q"))
	m.Update(key("r"))
	m.Update(key("s"))
	if len(sess.calls) != 0 {
		t.Fatalf("calls while stopping: %q", sess.calls)
	}
}

func TestRunningStoppingProgress(t *testing.T) {
	m, _ := newTestModel(t, 60, 8)
	m.Update(eventsMsg{events: []supervisor.Event{
		state("db", supervisor.Running), state("api", supervisor.Running),
	}})
	m.Update(key("q"))
	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Stopping)}})
	if got := screen(m)[0]; !strings.HasSuffix(got, "stopping… 2 left · ctrl+c to force") {
		t.Fatalf("header = %q", got)
	}
	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Stopped)}})
	if got := screen(m)[0]; !strings.HasSuffix(got, "stopping… 1 left · ctrl+c to force") {
		t.Fatalf("header = %q", got)
	}
}
