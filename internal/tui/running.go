package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"runr/internal/config"
	"runr/internal/supervisor"
)

// Session is the part of the supervisor the running view uses.
type Session interface {
	Processes() []supervisor.ProcessInfo
	Events() <-chan supervisor.Event
	StopAll() <-chan struct{}
	KillAll()
}

const (
	frameInterval  = time.Second / 30 // minimum time between event batches
	maxBatch       = 1 << 16          // events per batch, so up to ~2M lines/s
	drainIdle      = time.Millisecond // a batch ends when no event arrives for this long
	codeWidth      = 4                // sidebar space reserved for an exit code
	maxPrefixWidth = 16               // widest label in front of combined log lines
	minWidth       = 30
	minHeight      = 5
)

type (
	eventsMsg struct {
		events []supervisor.Event
		closed bool
	}
	stoppedMsg   struct{} // StopAll finished
	interruptMsg struct{} // a signal, treated like Ctrl+C
)

// sidebarEntry is a project shown in the sidebar.
type sidebarEntry struct {
	info  supervisor.ProcessInfo
	label string
	style lipgloss.Style // label color
}

// runningModel is the running view: a sidebar of processes and their
// combined log output.
type runningModel struct {
	sess  Session
	cfg   *config.Config
	title string

	entries    []*sidebarEntry
	byName     map[string]*sidebarEntry
	nextColor  int
	labelWidth int
	selected   int
	sideTop    int // first sidebar entry shown

	log       *logView
	lastBatch time.Time

	width, height int

	stopping bool
	forced   bool
}

func newRunningModel(sess Session, cfg *config.Config, title string) *runningModel {
	m := &runningModel{sess: sess, cfg: cfg, title: title, byName: map[string]*sidebarEntry{}}
	for _, info := range sess.Processes() {
		m.entry(info.Name).info = info
	}
	limit := cfg.Settings.ScrollbackLines() * max(len(m.entries), 1)
	m.log = newLogView(limit, m.formatCombined)
	return m
}

// entry returns the sidebar entry for a project, adding it if needed.
func (m *runningModel) entry(name string) *sidebarEntry {
	if e := m.byName[name]; e != nil {
		return e
	}
	e := &sidebarEntry{info: supervisor.ProcessInfo{Name: name}, label: name}
	if p := m.cfg.Projects[name]; p != nil {
		e.label = p.Label(name)
		e.style = lipgloss.NewStyle().Foreground(projectColor(p, &m.nextColor))
	}
	m.entries = append(m.entries, e)
	m.byName[name] = e
	m.labelWidth = max(m.labelWidth, ansi.StringWidth(e.label))
	if m.log != nil { // added after startup
		m.log.limit += m.cfg.Settings.ScrollbackLines()
		m.log.invalidate()
		m.layout()
	}
	return e
}

func (m *runningModel) Init() tea.Cmd {
	return m.waitEvents()
}

// waitEvents returns a command that delivers the next batch of supervisor
// events. Under heavy output it waits so that batches arrive at most once
// per frame; the view is redrawn once per batch rather than per line.
func (m *runningModel) waitEvents() tea.Cmd {
	events, last := m.sess.Events(), m.lastBatch
	return func() tea.Msg {
		e, ok := <-events
		if !ok {
			return eventsMsg{closed: true}
		}
		if wait := frameInterval - time.Since(last); wait > 0 {
			time.Sleep(wait)
		}
		batch := []supervisor.Event{e}
		var idle *time.Timer
		for len(batch) < maxBatch {
			select {
			case e, ok := <-events:
				if !ok {
					return eventsMsg{events: batch, closed: true}
				}
				batch = append(batch, e)
				continue
			default:
			}
			// The channel is refilled from the supervisor's queue as it is
			// drained, so it is often momentarily empty during a burst.
			if idle == nil {
				idle = time.NewTimer(drainIdle)
				defer idle.Stop()
			} else {
				idle.Reset(drainIdle)
			}
			select {
			case e, ok := <-events:
				if !ok {
					return eventsMsg{events: batch, closed: true}
				}
				batch = append(batch, e)
			case <-idle.C:
				return eventsMsg{events: batch}
			}
		}
		return eventsMsg{events: batch}
	}
}

func (m *runningModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
	case eventsMsg:
		m.lastBatch = time.Now()
		m.handleEvents(msg.events)
		if !msg.closed {
			return m, m.waitEvents()
		}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case interruptMsg:
		return m, m.interrupt()
	case stoppedMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m *runningModel) handleEvents(events []supervisor.Event) {
	var lines []supervisor.LogLine
	for _, e := range events {
		switch e := e.(type) {
		case supervisor.StateEvent:
			m.entry(e.Info.Name).info = e.Info
		case supervisor.LineEvent:
			m.entry(e.Line.Project)
			lines = append(lines, e.Line) // sanitized when shown
		}
	}
	if len(lines) > 0 {
		m.log.append(lines...)
	}
}

func (m *runningModel) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.interrupt()
	case "q":
		if !m.stopping {
			return m.interrupt()
		}
	case "up":
		m.selectEntry(m.selected - 1)
	case "down":
		m.selectEntry(m.selected + 1)
	case "pgup":
		m.log.pageUp()
	case "pgdown":
		m.log.pageDown()
	case "g", "home":
		m.log.scrollTop()
	case "G", "end":
		m.log.scrollBottom()
	case "f":
		m.log.toggleFollow()
	}
	return nil
}

// interrupt starts a graceful shutdown, or forces it if one is running.
func (m *runningModel) interrupt() tea.Cmd {
	if !m.stopping {
		m.stopping = true
		done := m.sess.StopAll()
		return func() tea.Msg {
			<-done
			return stoppedMsg{}
		}
	}
	if !m.forced {
		m.forced = true
		m.sess.KillAll()
	}
	return nil
}

func (m *runningModel) selectEntry(i int) {
	m.selected = max(min(i, len(m.entries)-1), 0)
	m.layout()
}

// sidebarWidth is the width of the sidebar column, without the divider.
func (m *runningModel) sidebarWidth() int {
	// " ▸ ● " + label + exit code + " "
	w := 5 + m.labelWidth + codeWidth + 1
	return max(min(w, m.width/3), 12)
}

// bodyHeight is the number of rows between the header and the help line.
func (m *runningModel) bodyHeight() int {
	return max(m.height-4, 0)
}

// layout updates sizes and scroll positions after a change.
func (m *runningModel) layout() {
	h := m.bodyHeight()
	m.log.setSize(m.width-m.sidebarWidth()-2, h)
	switch {
	case m.selected < m.sideTop:
		m.sideTop = m.selected
	case h > 0 && m.selected >= m.sideTop+h:
		m.sideTop = m.selected - h + 1
	}
	m.sideTop = max(min(m.sideTop, len(m.entries)-h), 0)
}

// formatCombined renders a line with its project's label in front.
func (m *runningModel) formatCombined(line supervisor.LogLine, width int) []string {
	e := m.entry(line.Project)
	lw := min(m.labelWidth, maxPrefixWidth)
	rows := wrap(sanitize(line.Text), width-lw-1)
	label := e.style.Render(pad(ansi.Truncate(e.label, lw, "…"), lw)) + " "
	indent := strings.Repeat(" ", lw+1)
	for i, row := range rows {
		if line.Stream == supervisor.System {
			row = faintStyle.Render(row)
		}
		if i == 0 {
			rows[i] = label + row
		} else {
			rows[i] = indent + row
		}
	}
	return rows
}

func (m *runningModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "runr · " + m.title
	return v
}

func (m *runningModel) render() string {
	if m.width == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return "terminal too small"
	}
	sw := m.sidebarWidth()
	logRows := m.log.view()

	var b strings.Builder
	b.WriteString(m.header())
	b.WriteByte('\n')
	b.WriteString(ruleStyle.Render(strings.Repeat("─", sw) + "┬" + strings.Repeat("─", m.width-sw-1)))
	b.WriteByte('\n')
	divider := ruleStyle.Render("│")
	for r := range m.bodyHeight() {
		b.WriteString(m.sidebarRow(m.sideTop+r, sw))
		b.WriteString(divider)
		b.WriteByte(' ')
		b.WriteString(logRows[r])
		b.WriteByte('\n')
	}
	b.WriteString(ruleStyle.Render(strings.Repeat("─", sw) + "┴" + strings.Repeat("─", m.width-sw-1)))
	b.WriteByte('\n')
	b.WriteString(m.help())
	return b.String()
}

func (m *runningModel) header() string {
	left := " " + boldStyle.Render("runr") + " · " + m.title
	var right string
	switch {
	case m.forced:
		right = warnStyle.Render("killing…")
	case m.stopping:
		right = warnStyle.Render("stopping… ctrl+c to force")
	case m.log.following():
		right = "combined · following"
	default:
		right = "combined · " + warnStyle.Render("paused")
		if n := m.log.below(); n > 0 {
			right += faintStyle.Render(fmt.Sprintf(" · %d more below", n))
		}
	}
	right += " "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return ansi.Truncate(left, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *runningModel) sidebarRow(i, width int) string {
	if i >= len(m.entries) {
		return strings.Repeat(" ", width)
	}
	e := m.entries[i]
	marker := " "
	style := e.style
	if i == m.selected {
		marker = "▸"
		style = style.Bold(true)
	}
	code := ""
	if e.info.State == supervisor.Crashed && e.info.Exit.Code > 0 {
		code = strconv.Itoa(e.info.Exit.Code)
	}
	labelWidth := width - 5 - 1 // " ▸ ● " and the right margin
	if code != "" {
		labelWidth -= len(code) + 1
	}
	label := ansi.Truncate(e.label, labelWidth, "…")
	row := " " + marker + " " + stateIcon(e.info.State) + " " + style.Render(label)
	if code != "" {
		row += " " + faintStyle.Render(code)
	}
	return pad(row, width)
}

func (m *runningModel) help() string {
	keys := [][2]string{
		{"↑↓", "select"}, {"pgup/pgdn", "scroll"}, {"g/G", "top/bottom"},
		{"f", "follow"}, {"q", "quit"},
	}
	if m.stopping {
		keys = [][2]string{{"ctrl+c", "force quit"}}
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, boldStyle.Render(k[0])+" "+faintStyle.Render(k[1]))
	}
	// When narrow, drop hints from the middle so quitting stays visible.
	line := " " + strings.Join(parts, "  ")
	for len(parts) > 2 && ansi.StringWidth(line) > m.width {
		parts = append(parts[:len(parts)-2], parts[len(parts)-1])
		line = " " + strings.Join(parts, "  ")
	}
	return ansi.Truncate(line, m.width, "…")
}

// pad pads s with spaces to width cells.
func pad(s string, width int) string {
	if n := width - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// Run shows the running view until the user quits and every process has
// stopped. title names what was started, e.g. "backend + web". Signals
// received on sigs are handled like Ctrl+C.
func Run(sess Session, cfg *config.Config, title string, sigs <-chan os.Signal) error {
	p := tea.NewProgram(newRunningModel(sess, cfg, title), tea.WithoutSignalHandler())
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-sigs:
				p.Send(interruptMsg{})
			case <-done:
				return
			}
		}
	}()
	_, err := p.Run()
	return err
}
