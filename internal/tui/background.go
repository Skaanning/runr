package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

// The launcher's Running section: sessions running in the background or in
// other terminals, which enter attaches to and x stops.

// Background reaches the sessions running elsewhere (cmd/runr: over their
// control sockets).
type Background interface {
	List() []RunningSession
	Attach(pid int) (Attached, error)
	Down(pid int) error // stops the session, as q does there, and waits until it has
}

// RunningSession describes a session running elsewhere.
type RunningSession struct {
	Pid      int
	Titles   []string // the groups and projects it was started with
	Started  time.Time
	Host     bool     // runs in the background (runr host)
	Attached int      // views attached to it
	States   []string // of its processes, e.g. "running"
}

// Attached is a session the launcher attached to, with what its running
// view shows.
type Attached struct {
	Session Session
	Config  *config.Config // as the session was started
	Titles  []string
}

// backgroundInterval is how often the Running section is refreshed.
const backgroundInterval = 2 * time.Second

type (
	backgroundMsg struct { // a refresh finished
		owner *backgroundTracker
		list  []RunningSession
	}
	backgroundTickMsg struct { // time for the next refresh
		owner *backgroundTracker
		gen   int
	}
	// attachMsg asks the app to attach to the session of pid.
	attachMsg struct{ pid int }
	// stoppedSessionMsg reports that stopping a session (x) finished.
	stoppedSessionMsg struct {
		owner *launcherModel
		pid   int
		err   error
	}
)

// backgroundTracker keeps the list of running sessions up to date, like
// gitTracker: one refresh at a time, every backgroundInterval after the
// last one finished. While paused (a running view has the screen) ticks
// don't refresh; resume catches up. A nil *backgroundTracker does nothing.
type backgroundTracker struct {
	bg     Background
	list   []RunningSession
	busy   bool // a refresh is running
	again  bool // refresh again once it finishes
	gen    int  // of the scheduled tick; older ticks are ignored
	paused bool
	missed bool // a tick came while paused
}

func newBackgroundTracker(bg Background) *backgroundTracker {
	if bg == nil {
		return nil
	}
	return &backgroundTracker{bg: bg}
}

// sessions returns the sessions of the last refresh.
func (t *backgroundTracker) sessions() []RunningSession {
	if t == nil {
		return nil
	}
	return t.list
}

// refresh lists the sessions in the background.
func (t *backgroundTracker) refresh() tea.Cmd {
	if t == nil {
		return nil
	}
	if t.busy {
		t.again = true
		return nil
	}
	t.busy = true
	bg := t.bg
	return func() tea.Msg { return backgroundMsg{owner: t, list: bg.List()} }
}

// update handles the tracker's messages, reporting whether it was one, and
// returns what to do next.
func (t *backgroundTracker) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case backgroundMsg:
		if t == nil || msg.owner != t {
			return nil, true
		}
		t.list, t.busy = msg.list, false
		if t.again {
			t.again = false
			return t.refresh(), true
		}
		t.gen++
		gen := t.gen
		return tea.Tick(backgroundInterval, func(time.Time) tea.Msg { return backgroundTickMsg{owner: t, gen: gen} }), true
	case backgroundTickMsg:
		if t == nil || msg.owner != t || msg.gen != t.gen {
			return nil, true
		}
		if t.paused {
			t.missed = true
			return nil, true
		}
		return t.refresh(), true
	}
	return nil, false
}

// pause stops refreshing until resume.
func (t *backgroundTracker) pause() {
	if t != nil {
		t.paused = true
	}
}

// resume refreshes again, right away: the list is old.
func (t *backgroundTracker) resume() tea.Cmd {
	if t == nil {
		return nil
	}
	t.paused, t.missed = false, false
	t.gen++ // a tick on its way is old
	return t.refresh()
}

// setSessions takes a new list of sessions, keeping the cursor on the
// session it was on (or the nearest one).
func (m *launcherModel) setSessions() {
	list := m.bg.sessions()
	if m.sessCursor < 0 {
		return
	}
	for i, s := range list {
		if s.Pid == m.sessPid {
			m.sessCursor = i
			return
		}
	}
	m.sessCursor = min(m.sessCursor, len(list)-1) // -1: back in the list below
	m.syncSessPid()
}

// syncSessPid remembers which session the cursor is on.
func (m *launcherModel) syncSessPid() {
	m.sessPid = 0
	if s, ok := m.currentSession(); ok {
		m.sessPid = s.Pid
	}
}

// currentSession returns the session under the cursor.
func (m *launcherModel) currentSession() (RunningSession, bool) {
	list := m.bg.sessions()
	if m.sessCursor >= 0 && m.sessCursor < len(list) {
		return list[m.sessCursor], true
	}
	return RunningSession{}, false
}

// preselectSession puts the cursor on the session of pid once the list
// has it (the next refresh): back from its view.
func (m *launcherModel) preselectSession(pid int) {
	m.sessCursor, m.sessPid = 0, pid
	for i, s := range m.bg.sessions() {
		if s.Pid == pid {
			m.sessCursor = i
		}
	}
}

// selectSession puts the cursor on the session at index i.
func (m *launcherModel) selectSession(i int) {
	m.sessCursor = i
	m.syncSessPid()
}

// attachSession asks the app to attach to the session, unless it is being
// stopped.
func (m *launcherModel) attachSession(s RunningSession) tea.Cmd {
	if m.stoppingSess[s.Pid] {
		m.notice = "session " + strconv.Itoa(s.Pid) + " is stopping"
		return nil
	}
	return func() tea.Msg { return attachMsg{pid: s.Pid} }
}

// confirmStopSession asks whether to stop the session under the cursor.
func (m *launcherModel) confirmStopSession() {
	s, ok := m.currentSession()
	if !ok || m.stoppingSess[s.Pid] {
		return
	}
	bg := m.bg.bg
	m.ask = &question{
		prompt: fmt.Sprintf("Stop session %d (%s)?", s.Pid, strings.Join(s.Titles, ", ")),
		action: "stop",
		yes: func() tea.Cmd {
			m.stoppingSess[s.Pid] = true
			m.notice = fmt.Sprintf("stopping session %d…", s.Pid)
			return func() tea.Msg { return stoppedSessionMsg{owner: m, pid: s.Pid, err: bg.Down(s.Pid)} }
		},
	}
}

// stoppedSession reports how stopping a session went, and refreshes the
// list.
func (m *launcherModel) stoppedSession(msg stoppedSessionMsg) tea.Cmd {
	delete(m.stoppingSess, msg.pid)
	m.notice = fmt.Sprintf("stopped session %d", msg.pid)
	if msg.err != nil {
		m.notice = fmt.Sprintf("can't stop session %d: %s", msg.pid, oneLine(msg.err.Error()))
	}
	return m.bg.refresh()
}

// sessionRows renders the Running section, and returns the row of the
// session under the cursor (-1 if it's not on one). It is empty when no
// session runs.
func (m *launcherModel) sessionRows() (rows []string, cursor int) {
	list := m.bg.sessions()
	cursor = -1
	if len(list) == 0 {
		return nil, cursor
	}
	rows = append(rows, strings.Repeat(" ", listIndent)+m.st.heading.Render("Running"))
	for i, s := range list {
		marker := strings.Repeat(" ", listIndent)
		if i == m.sessCursor {
			marker, cursor = " ▸ ", len(rows)
		}
		if m.jump && i < 9 {
			marker = " " + m.st.accent.Bold(true).Render(strconv.Itoa(i+1)) + " "
		}
		rows = append(rows, marker+m.sessionRow(s, i == m.sessCursor))
	}
	return append(rows, ""), cursor
}

// sessionRow renders a session, e.g. "● session 4123  backend, web ·
// 3 running · 1 crashed · since 10:42 · detached".
func (m *launcherModel) sessionRow(s RunningSession, current bool) string {
	counts := map[string]int{}
	for _, st := range s.States {
		counts[st]++
	}
	icon := m.st.faint.Render("●")
	switch {
	case counts[supervisor.Crashed.String()]+counts[supervisor.Blocked.String()] > 0:
		icon = m.st.err.Render("●")
	case counts[supervisor.Running.String()] > 0:
		icon = m.st.ok.Render("●")
	case len(s.States) > 0 && counts[supervisor.Stopped.String()]+counts[supervisor.Exited.String()] < len(s.States):
		icon = m.st.warn.Render("●")
	}
	name := "session " + strconv.Itoa(s.Pid)
	if current {
		name = m.st.bold.Render(name)
	}
	var parts []string
	for _, st := range []supervisor.State{
		supervisor.Running, supervisor.Starting, supervisor.Waiting, supervisor.Stopping,
		supervisor.Crashed, supervisor.Blocked, supervisor.Exited, supervisor.Stopped, supervisor.Pending,
	} {
		if n := counts[st.String()]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, st))
		}
	}
	if len(s.States) == 0 {
		parts = append(parts, "nothing started")
	}
	parts = append(parts, "since "+m.startedAt(s.Started))
	switch {
	case m.stoppingSess[s.Pid]:
		parts = append(parts, "stopping…")
	case s.Attached > 0:
		parts = append(parts, count(s.Attached, "view"))
	case s.Host:
		parts = append(parts, "detached")
	}
	row := icon + " " + name + "  " + strings.Join(s.Titles, ", ") + m.st.faint.Render(" · "+strings.Join(parts, " · "))
	return ansi.Truncate(row, max(m.width-listIndent, 1), "…")
}

// startedAt formats when a session started: the time if it was today, with
// the date otherwise.
func (m *launcherModel) startedAt(t time.Time) string {
	t, now := t.Local(), m.now()
	if y, mo, d := t.Date(); y == now.Year() && mo == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}
