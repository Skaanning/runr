package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/supervisor"
)

type fakeSession struct {
	procs    []supervisor.ProcessInfo
	events   chan supervisor.Event
	logs     *supervisor.Logs
	stopDone chan struct{}
	stopAll  int
	killAll  int
	calls    []string            // Start/Stop/Restart calls, e.g. "stop api"
	notes    map[string][]string // lines passed to Note, by project
	err      error               // returned by Start/Stop/Restart
	pids     map[string][]int    // returned by Pids
	seen     map[string][]int    // passed to RememberPorts
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
func (f *fakeSession) Events() <-chan supervisor.Event      { return f.events }
func (f *fakeSession) StopAll() <-chan struct{}             { f.stopAll++; return f.stopDone }
func (f *fakeSession) KillAll()                             { f.killAll++ }
func (f *fakeSession) Pids() map[string][]int               { return f.pids }
func (f *fakeSession) RememberPorts(ports map[string][]int) { f.seen = ports }
func (f *fakeSession) Note(name string, lines ...string) error {
	if f.notes == nil {
		f.notes = map[string][]string{}
	}
	f.notes[name] = append(f.notes[name], lines...)
	return nil
}

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
	m := newRunningModel(sess, testConfig(), []string{"backend"}, newStyles(terminalTheme))
	m.now = testNow
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
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
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
		"  runr   backend       ● 1  ⠋ 1  ✗ 1   combined · following",
		"───────────────────┬────────────────────────────────────────",
		" ▌ ● db            │ db     starting: db",
		"   ⠋ api           │ api    listening on :8080",
		"   ✗ Worker exit 1 │ Worker error: queue not found",
		"                   │",
		"───────────────────┴────────────────────────────────────────",
		"  ↑↓  select  tab  view  r  restart  s  stop  q  quit",
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
	m := newRunningModel(sess, cfg, []string{"all"}, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 7}) // 3 body rows

	m.Update(key("up")) // stays at the first entry
	if m.selected != 0 {
		t.Fatalf("selected = %d", m.selected)
	}
	for range 4 {
		m.Update(key("down"))
	}
	if got := screen(m)[2:5]; !strings.HasPrefix(got[0], "   ○ p2") || !strings.HasPrefix(got[2], " ▌ ○ p4") {
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

func TestRunningSidebarScrollTwoLines(t *testing.T) {
	sess := newFakeSession()
	cfg := &config.Config{Projects: map[string]*config.Project{}}
	for i := range 10 {
		name := fmt.Sprintf("p%d", i)
		cfg.Projects[name] = &config.Project{Cmd: "x"}
		sess.procs = append(sess.procs, supervisor.ProcessInfo{Name: name})
	}
	m := newRunningModel(sess, cfg, []string{"all"}, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 11}) // panes: 7 rows, 3 processes
	for range 4 {
		m.Update(key("down"))
	}
	want := []string{"│  ○ p2 ▁▁▁▁▁▁▁▁", "│", "│  ○ p3 ▁▁▁▁▁▁▁▁", "│", "│ ▌○ p4 ▁▁▁▁▁▁▁▁", "│ ▌", "│"}
	got := screen(m)[1:8]
	for i, w := range want {
		if side, _, _ := strings.Cut(got[i], "││"); strings.TrimRight(side, " ") != w {
			t.Fatalf("sidebar:\n%s\nwant rows starting %q", strings.Join(got, "\n"), want)
		}
	}
	for range 20 {
		m.Update(key("down"))
	}
	if m.selected != 9 || m.sideTop != 7 {
		t.Fatalf("selected = %d, sideTop = %d", m.selected, m.sideTop)
	}
	m.Update(key("!"))                                 // no toast: nothing changes
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24}) // everything fits again
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
	// sidebar 12, log column 40-12-2 = 26 cells, label column 7 → 19 cells of text
	m.Update(eventsMsg{events: []supervisor.Event{line("api", "abcdefghijklmnopqrstuvwxyz")}})
	got := screen(m)
	for i, want := range []string{"│ api    abcdefghijklmnopqrs", "│        tuvwxyz"} {
		if !strings.HasSuffix(got[2+i], want) {
			t.Errorf("row %d = %q, want suffix %q", 2+i, got[2+i], want)
		}
	}
}

func TestRunningNewProjectFromEvents(t *testing.T) {
	m, _ := newTestModel(t, 60, 14)
	m.cfg.Projects["frontend-application"] = &config.Project{Cmd: "x"}
	m.Update(eventsMsg{events: []supervisor.Event{
		state("frontend-application", supervisor.Waiting),
		line("frontend-application", "hi"),
	}})
	got := screen(m)
	if !strings.HasPrefix(got[7], "│  ⠋ frontend-applicati… ││") {
		t.Fatalf("sidebar row = %q", got[7])
	}
	if !strings.Contains(got[1], "││ frontend-applic… hi") {
		t.Fatalf("log row = %q", got[1])
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
	body, divider := rows[2:len(rows)-2], "│"
	if m.panes() {
		body, divider = rows[1:len(rows)-3], "││"
	}
	for _, r := range body {
		_, text, _ := strings.Cut(r, divider)
		text = strings.TrimSuffix(text, "│")
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
	if got := screen(m)[0]; !strings.Contains(got, "─ logs · db ─") || !strings.HasSuffix(got, " following ─╮") {
		t.Fatalf("top border = %q", got)
	}
	if got := screen(m)[len(screen(m))-1]; !strings.Contains(got, "tab  all") {
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
	if got := screen(m)[0]; !strings.Contains(got, "─ logs · api ─") {
		t.Fatalf("top border = %q", got)
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
	if !strings.Contains(helpRow(), "s  start") {
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

func typeText(m *runningModel, text string) {
	for _, r := range text {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// statusRow returns the status bar of the pane layout.
func statusRow(m *runningModel) string {
	rows := screen(m)
	return rows[len(rows)-2]
}

func helpRow(m *runningModel) string {
	rows := screen(m)
	return rows[len(rows)-1]
}

func TestRunningSearch(t *testing.T) {
	m, sess := newTestModel(t, 80, 8) // 4 body rows
	var events []supervisor.Event
	for i := range 10 {
		text := fmt.Sprint("line ", i)
		if i%3 == 2 {
			text += " Error"
		}
		events = append(events, emit(sess, "db", text))
	}
	m.Update(eventsMsg{events: events}) // shows lines 6-9

	m.Update(key("/"))
	typeText(m, "error q") // q must not quit
	if sess.stopAll != 0 {
		t.Fatal("q in the search input quit")
	}
	for range 2 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	if got := helpRow(m); !strings.HasPrefix(got, " /error") || !strings.Contains(got, "enter  search") {
		t.Fatalf("help while editing = %q", got)
	}
	if !strings.Contains(m.render(), "line 8 "+m.st.match+"Error\x1b[0m") {
		t.Fatal("matches not highlighted while typing")
	}

	m.Update(key("enter")) // newest match on screen
	if !strings.Contains(m.render(), m.st.current+"Error") || !strings.HasPrefix(helpRow(m), " /error 3/3  n/N  next/prev  esc  clear") {
		t.Fatalf("after enter: help = %q", helpRow(m))
	}

	m.Update(key("N"))
	if got := strings.Join(logRows(m), ","); !strings.Contains(got, "line 5 Error") {
		t.Fatalf("match not revealed: %q", got)
	}
	if !strings.HasPrefix(helpRow(m), " /error 2/3") || !strings.Contains(screen(m)[0], "paused") {
		t.Fatalf("help = %q, header = %q", helpRow(m), screen(m)[0])
	}
	m.Update(key("N"))
	m.Update(key("N"))
	if got := helpRow(m); got != " search wrapped to the bottom" {
		t.Fatalf("help = %q", got)
	}
	m.Update(key("n"))
	if got := helpRow(m); got != " search wrapped to the top" {
		t.Fatalf("help = %q", got)
	}
	if !strings.Contains(strings.Join(logRows(m), ","), "line 2 Error") {
		t.Fatalf("rows = %q", logRows(m))
	}

	// Opening the input and cancelling keeps the search.
	m.Update(key("/"))
	typeText(m, "zzz")
	m.Update(key("esc"))
	if !strings.HasPrefix(helpRow(m), " /error") {
		t.Fatalf("search not restored: %q", helpRow(m))
	}

	m.Update(key("esc"))
	if strings.Contains(m.render(), m.st.match) || strings.Contains(helpRow(m), "/error") {
		t.Fatal("esc did not clear the search")
	}

	m.Update(key("/"))
	typeText(m, "Line") // capital letter: exact case, so nothing matches
	m.Update(key("enter"))
	if got := helpRow(m); got != ` no matches for "Line"` {
		t.Fatalf("help = %q", got)
	}
}

func TestRunningSearchSingleView(t *testing.T) {
	m, sess := newTestModel(t, 80, 8)
	m.Update(eventsMsg{events: []supervisor.Event{
		emit(sess, "db", "db hit"), emit(sess, "api", "api hit"), emit(sess, "db", "db miss"),
	}})
	m.Update(key("/"))
	typeText(m, "hit")
	m.Update(key("enter"))
	if !strings.HasPrefix(helpRow(m), " /hit 2/2") {
		t.Fatalf("combined: %q", helpRow(m))
	}
	m.Update(key("tab")) // db only: one match, search kept
	m.Update(key("n"))
	if !strings.HasPrefix(helpRow(m), " /hit 1/1") {
		t.Fatalf("single: %q", helpRow(m))
	}
	if !strings.Contains(m.render(), "db "+m.st.current+"hit") {
		t.Fatal("current match not highlighted in the single view")
	}
}

func TestRunningSearchKeepsFaintSystemLines(t *testing.T) {
	m, _ := newTestModel(t, 80, 8)
	m.Update(eventsMsg{events: []supervisor.Event{system("db", "starting: db --port 1")}})
	m.Update(key("/"))
	typeText(m, "db")
	out := m.render()
	// After the highlighted match the faint styling is restored.
	if !strings.Contains(out, m.st.system+"starting: "+m.st.match+"db\x1b[0m"+m.st.system+" --port 1") {
		t.Fatalf("render = %q", out)
	}
}

// pickerConfig has projects outside the session, with dependencies and a
// missing directory, and groups.
func pickerConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		Projects: map[string]*config.Project{
			"db":     {Dir: dir, Cmd: "db"},
			"api":    {Dir: dir, Cmd: "api", DependsOn: []string{"db"}},
			"worker": {Dir: dir, Cmd: "worker", DependsOn: []string{"db"}, DisplayName: "Worker"},
			"docs":   {Dir: dir + "/missing", Cmd: "docs"},
			"cron":   {Dir: dir, Cmd: "cron"},
		},
		Groups: map[string][]string{
			"backend": {"db", "api"},
			"tools":   {"worker", "docs"},
			"jobs":    {"worker"},
			"empty":   {},
		},
	}
}

// newPickerModel has the sidebar list only the session's projects, so the
// picker lists the others.
func newPickerModel(t *testing.T) (*runningModel, *fakeSession) {
	t.Helper()
	cfg := pickerConfig(t)
	off := false
	cfg.Settings.ShowAllValue = &off
	sess := newFakeSession("db", "api")
	m := newRunningModel(sess, cfg, []string{"backend"}, newStyles(terminalTheme))
	m.now = testNow
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running), state("api", supervisor.Running)}})
	return m, sess
}

func TestPickerItems(t *testing.T) {
	m, _ := newPickerModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.Update(key("+"))
	want := []string{
		"›   type to filter                                5 items",
		"Groups",
		"▌  jobs    worker",
		"tools   worker, docs",
		"Projects",
		"● cron",
		"● docs",
		"● Worker  needs db",
	}
	if got := logRows(m); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("picker:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := helpRow(m); got != "  type  to filter  ↑↓  select  enter  start  esc  cancel" {
		t.Fatalf("help = %q", got)
	}

	typeText(m, "WOR") // matches the display name, ignoring case; q-like keys are text
	if got := strings.Join(logRows(m), ","); !strings.HasPrefix(got, "› WOR ") || !strings.HasSuffix(got, " 1/5,Projects,▌● Worker  needs db") {
		t.Fatalf("filtered = %q", got)
	}
	if !strings.Contains(m.render(), m.st.accent.Bold(true).Render("Wor")) {
		t.Error("the match is not highlighted")
	}
	typeText(m, "x")
	if got := logRows(m)[1]; got != "no matches" {
		t.Fatalf("filtered = %q", got)
	}
	m.Update(key("esc"))
	if m.picker != nil || len(logRows(m)) > 0 {
		t.Fatal("esc did not close the picker")
	}
}

func TestPickerStartsProject(t *testing.T) {
	m, sess := newPickerModel(t)
	m.Update(key("+"))
	typeText(m, "wor")
	m.Update(key("enter"))
	if got := strings.Join(sess.calls, ","); got != "start worker" {
		t.Fatalf("calls = %q", got)
	}
	if m.picker != nil {
		t.Fatal("picker still open")
	}
	got := screen(m)
	if got := statusRow(m); !strings.HasPrefix(got, "  FOLLOW  backend + worker ") {
		t.Fatalf("status bar = %q", got)
	}
	if !strings.HasPrefix(got[5], "│ ▌○ Worker") {
		t.Fatalf("sidebar row = %q, want the new project selected", got[5])
	}
}

func TestPickerStartsGroup(t *testing.T) {
	m, sess := newPickerModel(t)
	m.Update(key("+"))
	m.Update(key("enter")) // jobs
	if got := strings.Join(sess.calls, ","); got != "start jobs" {
		t.Fatalf("calls = %q", got)
	}
	if got := statusRow(m); !strings.HasPrefix(got, "  FOLLOW  backend + jobs ") {
		t.Fatalf("status bar = %q", got)
	}
	if m.selected != 0 {
		t.Fatal("starting a group changed the selection")
	}

	// The project is now in the session; picking it again (e.g. after a crash)
	// leaves the title alone.
	m.Update(eventsMsg{events: []supervisor.Event{state("worker", supervisor.Crashed)}})
	m.Update(key("+"))
	typeText(m, "worker")
	if got := logRows(m)[2]; !strings.HasPrefix(got, "▌● ✗ Worker  needs db") || !strings.HasSuffix(got, "crashed") {
		t.Fatalf("item = %q", got)
	}
	m.Update(key("enter"))
	if got := statusRow(m); !strings.HasPrefix(got, "  FOLLOW  backend + jobs ") {
		t.Fatalf("status bar = %q", got)
	}
}

func TestPickerMissingDir(t *testing.T) {
	m, sess := newPickerModel(t)
	m.Update(key("+"))
	m.Update(key("down"))
	m.Update(key("enter")) // tools: docs has no dir
	if len(sess.calls) != 0 {
		t.Fatalf("calls = %q", sess.calls)
	}
	if got := helpRow(m); !strings.HasPrefix(got, ` cannot start: project "docs": dir: `) {
		t.Fatalf("help = %q", got)
	}
	if !strings.HasPrefix(statusRow(m), "  FOLLOW  backend ") {
		t.Fatalf("status bar = %q", statusRow(m))
	}
}

func TestPickerCtrlCQuits(t *testing.T) {
	m, sess := newPickerModel(t)
	m.Update(key("+"))
	typeText(m, "q")
	if sess.stopAll != 0 {
		t.Fatal("q in the filter quit")
	}
	m.Update(key("ctrl+c"))
	if sess.stopAll != 1 || m.picker != nil {
		t.Fatalf("stopAll = %d, picker open = %v", sess.stopAll, m.picker != nil)
	}
	m.Update(key("+"))
	if m.picker != nil {
		t.Fatal("picker opened while stopping")
	}
}

func TestRunningPasteSearchesAndFilters(t *testing.T) {
	m, sess := newTestModel(t, 80, 8)
	m.Update(eventsMsg{events: []supervisor.Event{emit(sess, "db", "line 1"), emit(sess, "db", "line 2 Error")}})
	m.Update(key("/"))
	m.Update(tea.PasteMsg{Content: "Error"})
	if !strings.Contains(m.render(), "line 2 "+m.st.match+"Error\x1b[0m") {
		t.Fatal("a pasted search isn't highlighted")
	}

	m, _ = newPickerModel(t)
	m.Update(key("+"))
	m.Update(tea.PasteMsg{Content: "cron"})
	if it, ok := m.picker.selected(); !ok || it.name != "cron" || len(m.picker.filtered) != 1 {
		t.Fatalf("after pasting a filter: %d items, on %q", len(m.picker.filtered), it.name)
	}
}
