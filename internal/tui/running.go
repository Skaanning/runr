package tui

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

// Session is the part of the supervisor the running view uses.
type Session interface {
	Processes() []supervisor.ProcessInfo
	Events() <-chan supervisor.Event
	Logs() *supervisor.Logs
	Start(names ...string) error
	Stop(name string) error
	Restart(name string) error
	StopAll() <-chan struct{}
	KillAll()
	Note(name string, lines ...string) error
	Pids() map[string][]int // process trees of the live processes, by project
	RememberPorts(ports map[string][]int)
}

const (
	frameInterval  = time.Second / 30 // minimum time between event batches
	maxBatch       = 1 << 16          // events per batch, so up to ~2M lines/s
	drainIdle      = time.Millisecond // a batch ends when no event arrives for this long
	maxPrefixWidth = 16               // widest label in front of combined log lines
	minWidth       = 30
	minHeight      = 5
	paneMinWidth   = 50 // below this, or paneMinHeight, the view has no panes
	paneMinHeight  = 10
)

type (
	eventsMsg struct {
		events []supervisor.Event
		closed bool
	}
	stoppedMsg   struct{}          // StopAll finished
	interruptMsg struct{}          // a signal, treated like Ctrl+C
	uiTickMsg    struct{ gen int } // time to redraw spinners and uptimes
)

const (
	animateEvery = 100 * time.Millisecond // tick while a spinner turns
	uptimeEvery  = time.Second            // tick while uptimes count
	statusWidth  = 6                      // minimum width of the uptime column
)

// sidebarEntry is a project shown in the sidebar.
type sidebarEntry struct {
	info    supervisor.ProcessInfo
	label   string
	color   string         // the project's color, configured or a default
	style   lipgloss.Style // label color, from color
	rate    outputRate     // the project's output lines per second
	oneShot bool           // ready = "exit": shows "done" once it exited
}

// runningModel is the running view: a sidebar of processes next to either
// their combined log output or the output of the selected process.
type runningModel struct {
	sess   Session
	cfg    *config.Config
	st     *styles
	titles []string // the groups and projects started, for the header

	entries    []*sidebarEntry
	byName     map[string]*sidebarEntry
	labelWidth int
	selected   int // index into entries, then idle
	sideTop    int // first sidebar entry shown

	showAll   bool     // the sidebar lists the projects not started too
	idle      []string // registered projects not in the session, sorted; empty unless showAll
	idleWidth int      // widest label in idle
	idleTop   int      // first of idle shown

	combined  *logView // always kept up to date, so switching back is instant
	single    *logView // the selected process's output; nil in the combined view
	singleFor string   // project shown in single
	snapSeq   uint64   // newest line in the snapshot single was built from
	lastBatch time.Time
	notice    string  // result of the last action, shown until the next key
	toasts    []toast // crashes and blocks, oldest first

	search     search // applies to both views
	prevSearch search // restored when editing is cancelled
	editing    bool   // the search input is open
	input      textinput.Model
	matchInfo  string // position of the current match, e.g. "3/17"

	picker  *picker   // open picker (start another, branches), or nil
	confirm *question // open question in the help line, or nil

	git       *gitTracker // nil: git is not installed
	gitAction string      // git action running in the background, e.g. "git switch"
	ports     *portTracker
	saveDir   string // where w saves logs; "" for the current directory

	width, height int
	now           func() time.Time // for the clock, uptimes and spinners
	tickEvery     time.Duration    // interval of the scheduled uiTickMsg, 0 if none
	tickGen       int              // generation of the scheduled uiTickMsg

	stopping bool
	forced   bool
}

func newRunningModel(sess Session, cfg *config.Config, names []string, st *styles) *runningModel {
	m := &runningModel{sess: sess, cfg: cfg, st: st, titles: names, byName: map[string]*sidebarEntry{}, now: time.Now}
	m.ports = newPortTracker(sess.Pids)
	m.showAll = cfg.Settings.ShowAll()
	for _, info := range sess.Processes() {
		m.entry(info.Name).info = info
	}
	m.refreshIdle()
	limit := cfg.Settings.ScrollbackLines() * max(len(m.entries), 1)
	m.combined = newLogView(limit, m.formatCombined)

	m.input = textinput.New()
	m.input.Prompt = ""
	styles := textinput.DefaultStyles(true)
	styles.Cursor.Blink = false
	m.input.SetStyles(styles)
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
		e.color = m.cfg.ColorOf(name)
		e.style = lipgloss.NewStyle().Foreground(m.st.color(e.color))
		e.oneShot = p.OneShot()
	}
	m.entries = append(m.entries, e)
	m.byName[name] = e
	m.labelWidth = max(m.labelWidth, ansi.StringWidth(e.label))
	if m.combined != nil { // added after startup
		m.combined.limit += m.cfg.Settings.ScrollbackLines()
		m.combined.invalidate()
		m.refreshIdle()
		m.layout()
	}
	return e
}

// restyle resolves the projects' colors again after the palette changed.
func (m *runningModel) restyle() {
	for _, e := range m.entries {
		if e.color != "" {
			e.style = lipgloss.NewStyle().Foreground(m.st.color(e.color))
		}
	}
	m.combined.invalidate()
	if m.single != nil {
		m.single.invalidate()
	}
}

// view returns the log view on screen.
func (m *runningModel) view() *logView {
	if m.single != nil {
		return m.single
	}
	return m.combined
}

// showSingle switches to the output of one project. The view starts from
// the supervisor's buffer; events for lines already in it are skipped.
func (m *runningModel) showSingle(name string) {
	lines := m.sess.Logs().Lines(name)
	m.single = newLogView(m.cfg.Settings.ScrollbackLines(), m.formatSingle)
	m.single.append(lines...)
	m.singleFor, m.snapSeq = name, 0
	for _, l := range lines {
		m.snapSeq = max(m.snapSeq, l.Seq)
	}
	m.layout()
}

func (m *runningModel) Init() tea.Cmd {
	return tea.Batch(m.waitEvents(), m.git.refresh(m.gitDirs()), m.ports.refresh(), tea.RequestBackgroundColor, clockTick(m, m.now()))
}

// gitDirs returns the directories of the projects in the sidebar.
func (m *runningModel) gitDirs() map[string]string {
	names := slices.Clone(m.idle)
	for _, e := range m.entries {
		names = append(names, e.info.Name)
	}
	return projectDirs(m.cfg, names)
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
	n := len(m.entries)
	m.pruneToasts()
	cmd := m.update(msg)
	if len(m.entries) > n && m.combined != nil {
		// Projects were added (e.g. with the picker): show their git state too.
		cmd = tea.Batch(cmd, m.git.refresh(m.gitDirs()))
	}
	return m, tea.Batch(cmd, m.scheduleTick())
}

// wantTick returns how often the view needs redrawing without other
// events: often while a spinner turns, every second while uptimes count or
// sparklines move, never when nothing changes.
func (m *runningModel) wantTick() time.Duration {
	every := time.Duration(0)
	now := m.now().Unix()
	for _, e := range m.entries {
		switch e.info.State {
		case supervisor.Starting, supervisor.Waiting, supervisor.Stopping:
			return animateEvery
		case supervisor.Running:
			every = uptimeEvery
		}
		if e.rate.recent(now) {
			every = uptimeEvery
		}
	}
	return every
}

// scheduleTick starts a tick of the wanted speed when it changed. Ticks of
// an older generation are ignored, so they never stack.
func (m *runningModel) scheduleTick() tea.Cmd {
	want := m.wantTick()
	if want == m.tickEvery {
		return nil
	}
	m.tickGen++
	m.tickEvery = want
	return m.tick()
}

func (m *runningModel) tick() tea.Cmd {
	if m.tickEvery == 0 {
		return nil
	}
	gen := m.tickGen
	return tea.Tick(m.tickEvery, func(time.Time) tea.Msg { return uiTickMsg{gen: gen} })
}

func (m *runningModel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
	case tea.ColorProfileMsg:
		m.st.setProfile(msg.Profile)
		m.restyle()
	case tea.BackgroundColorMsg:
		m.st.setDark(msg.IsDark())
		m.restyle()
	case clockMsg:
		if msg.owner == m {
			return clockTick(m, m.now())
		}
	case uiTickMsg:
		if msg.gen == m.tickGen {
			m.layout() // uptimes can widen their column
			return m.tick()
		}
	case eventsMsg:
		m.lastBatch = time.Now()
		cmd := m.handleEvents(msg.events)
		if !msg.closed {
			return tea.Batch(cmd, m.waitEvents())
		}
		return cmd
	case toastMsg: // expired toasts are pruned in Update
	case gitMsg:
		cmd := m.git.update(msg, m.gitDirs)
		m.layout() // the git column may have changed width
		return cmd
	case gitTickMsg:
		if !m.stopping {
			return m.git.update(msg, m.gitDirs)
		}
	case portsMsg:
		cmd := m.ports.update(msg, m.alive() > 0 && !m.stopping)
		m.layout() // the second lines may have changed width
		if msg.owner == m.ports && len(msg.ports) > 0 {
			// So that a later start warns when another process took one.
			sess, seen := m.sess, msg.ports
			cmd = tea.Batch(cmd, func() tea.Msg { sess.RememberPorts(seen); return nil })
		}
		return cmd
	case savedLogMsg:
		if msg.owner == m {
			m.notice = savedNotice(msg)
		}
	case openedMsg:
		if msg.owner == m {
			m.notice = "opened " + msg.url
			if msg.err != nil {
				m.notice = "can't open " + msg.url + ": " + msg.err.Error()
			}
		}
	case portTickMsg:
		if !m.stopping {
			return m.ports.update(msg, true)
		}
	case branchesMsg:
		if msg.owner == m {
			return m.showBranches(msg)
		}
	case switchedMsg:
		if msg.owner == m {
			return m.switched(msg)
		}
	case pullCheckMsg:
		if msg.owner == m {
			m.confirmPull(msg)
		}
	case pulledMsg:
		if msg.owner == m {
			return m.pulled(msg)
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case interruptMsg:
		return m.interrupt()
	case stoppedMsg:
		return tea.Quit
	default:
		var cmd tea.Cmd
		switch {
		case m.picker != nil:
			m.picker.input, cmd = m.picker.input.Update(msg)
		case m.editing:
			m.input, cmd = m.input.Update(msg)
		}
		return cmd
	}
	return nil
}

// handleEvents applies a batch of supervisor events. It returns a command
// to expire the toasts it showed.
func (m *runningModel) handleEvents(events []supervisor.Event) tea.Cmd {
	var lines, single []supervisor.LogLine
	states, toasts := false, false
	sec := m.now().Unix()
	for _, e := range events {
		switch e := e.(type) {
		case supervisor.StateEvent:
			entry := m.entry(e.Info.Name)
			prev := entry.info.State
			entry.info = e.Info
			states = true
			if t, ok := m.newToast(prev, e.Info); ok {
				m.addToast(t)
				toasts = true
			}
		case supervisor.WarningEvent:
			m.entry(e.Project)
			if !m.stopping {
				m.addToast(toast{name: e.Project, warn: true, what: e.Text, until: m.now().Add(toastFor)})
				toasts = true
			}
		case supervisor.LineEvent:
			if entry := m.entry(e.Line.Project); !e.Line.Stream.Runr() {
				entry.rate.add(sec, 1)
			}
			lines = append(lines, e.Line) // sanitized when shown
			// Events can arrive slightly out of Seq order, so only lines
			// covered by the snapshot are skipped.
			if m.single != nil && e.Line.Project == m.singleFor && e.Line.Seq > m.snapSeq {
				single = append(single, e.Line)
			}
		}
	}
	if len(lines) > 0 {
		m.combined.append(lines...)
	}
	if len(single) > 0 {
		m.single.append(single...)
	}
	var cmd tea.Cmd
	if states {
		m.layout() // exit codes change the sidebar's width
		if !m.stopping {
			cmd = m.ports.refresh() // started processes may listen soon, ended ones don't
		}
	}
	if toasts {
		return tea.Batch(cmd, tea.Tick(toastFor, func(time.Time) tea.Msg { return toastMsg{} }))
	}
	return cmd
}

func (m *runningModel) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	m.notice = ""
	toasts := m.toasts // any key dismisses them
	m.toasts = nil
	switch {
	case m.confirm != nil:
		return m.handleConfirmKey(msg)
	case m.picker != nil:
		return m.handlePickerKey(msg)
	case m.editing:
		return m.handleSearchKey(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		return m.interrupt()
	case "q":
		if !m.stopping {
			return m.interrupt()
		}
	case "!": // select the project of the newest toast
		if len(toasts) > 0 {
			name := toasts[len(toasts)-1].name
			if i := slices.IndexFunc(m.entries, func(e *sidebarEntry) bool { return e.info.Name == name }); i >= 0 {
				m.selectEntry(i)
			}
		}
	case "up":
		m.selectEntry(m.selected - 1)
	case "down":
		m.selectEntry(m.selected + 1)
	case "tab":
		if m.single != nil {
			m.single = nil
			m.layout()
		} else if name := m.selectedName(); name != "" {
			m.showSingle(name)
		}
	case "a":
		m.toggleAll()
		if m.showAll {
			return m.git.refresh(m.gitDirs())
		}
	case "enter":
		if name := m.selectedIdle(); name != "" {
			m.startIdle(name)
		}
	case "+":
		if m.stopping {
			return nil
		}
		empty := "everything is running"
		if m.showAll {
			empty = "no groups to start (projects start from the sidebar)"
		}
		var cmd tea.Cmd
		m.picker, cmd = newPicker(m.st, &picker{
			title: "start", action: "start", empty: empty, noun: "item", items: m.pickerItems(),
			pick: func(it pickerItem) tea.Cmd { m.startPicked(it); return nil },
		})
		return cmd
	case "/":
		m.editing, m.prevSearch = true, m.search
		m.input.Reset()
		m.setSearch(search{})
		return m.input.Focus()
	case "n":
		m.jump(1)
	case "N":
		m.jump(-1)
	case "esc":
		if m.search.active() {
			m.setSearch(search{})
		}
	case "b":
		return m.openBranches()
	case "p":
		return m.openPull()
	case "o":
		return m.openPorts()
	case "w":
		return m.saveLog()
	case "r":
		m.restart()
	case "s":
		m.stopOrStart()
	case "pgup":
		m.view().pageUp()
	case "pgdown":
		m.view().pageDown()
	case "g", "home":
		m.view().scrollTop()
	case "G", "end":
		m.view().scrollBottom()
	case "f":
		m.view().toggleFollow()
	}
	return nil
}

// handlePickerKey handles keys while a picker is open.
func (m *runningModel) handlePickerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.picker = nil
		return m.interrupt()
	case "esc":
		if m.picker.cancel != nil {
			m.picker.cancel()
		}
		m.picker = nil
	case "enter":
		if it, ok := m.picker.selected(); ok {
			pick := m.picker.pick
			m.picker = nil
			return pick(it)
		}
	default:
		return m.picker.update(msg)
	}
	return nil
}

// handleConfirmKey handles keys while a question is open: enter or y
// answers yes; esc, n or q cancel.
func (m *runningModel) handleConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		m.confirm = nil
		return m.interrupt()
	}
	done, cmd := m.confirm.answer(msg.String())
	if done {
		m.confirm = nil
	}
	return cmd
}

// pickerItems lists the groups with something left to start and, unless
// the sidebar lists every project, the projects that are not running or
// waiting to run.
func (m *runningModel) pickerItems() []pickerItem {
	running := func(name string) bool {
		e := m.byName[name]
		return e != nil && canStop(e.info.State)
	}
	var items []pickerItem
	for _, g := range slices.Sorted(maps.Keys(m.cfg.Groups)) {
		resolved, err := m.cfg.Resolve(g)
		if err != nil || !slices.ContainsFunc(resolved, func(n string) bool { return !running(n) }) {
			continue
		}
		items = append(items, pickerItem{
			name: g, label: g, group: true, section: "Groups", info: strings.Join(m.cfg.Groups[g], ", "),
		})
	}
	for _, name := range slices.Sorted(maps.Keys(m.cfg.Projects)) {
		if running(name) || m.showAll {
			continue
		}
		p := m.cfg.Projects[name]
		it := pickerItem{name: name, label: p.Label(name), section: "Projects", mark: m.dot(name)}
		if e := m.byName[name]; e != nil && e.info.State != supervisor.Pending {
			it.mark += " " + m.st.stateIcon(e.info.State, spinner(m.now()))
			it.right = e.info.State.String()
		}
		if len(p.DependsOn) > 0 {
			it.info = "needs " + strings.Join(p.DependsOn, ", ")
		}
		items = append(items, it)
	}
	return items
}

// dot renders a dot in the project's color.
func (m *runningModel) dot(name string) string {
	return lipgloss.NewStyle().Foreground(m.st.color(m.cfg.ColorOf(name))).Render("●")
}

// startPicked starts a group or project chosen in the picker, with its
// dependencies, and adds it to the sidebar and the title.
func (m *runningModel) startPicked(it pickerItem) {
	resolved, err := m.cfg.Resolve(it.name)
	if err == nil {
		err = m.checkDirs(resolved)
	}
	if err == nil {
		err = m.sess.Start(it.name)
	}
	if err != nil {
		m.report(err)
		return
	}
	known := m.byName[it.name] != nil
	for _, name := range resolved { // in the supervisor's order: dependencies first
		m.entry(name)
	}
	if !slices.Contains(m.titles, it.name) && (it.group || !known) {
		m.titles = append(m.titles, it.name)
	}
	if !it.group {
		m.selectEntry(m.indexOf(it.name))
	}
}

// checkDirs reports projects that would be started but whose working
// directory is missing, like runr up does before starting anything.
func (m *runningModel) checkDirs(projects []string) error {
	warnings, _ := m.cfg.Validate()
	var missing []string
	for _, w := range warnings {
		if e := m.byName[w.Name]; w.Field == "dir" && slices.Contains(projects, w.Name) && (e == nil || !canStop(e.info.State)) {
			missing = append(missing, w.String())
		}
	}
	if len(missing) > 0 {
		return errors.New("cannot start: " + strings.Join(missing, "; "))
	}
	return nil
}

// handleSearchKey handles keys while the search input is open. Matches are
// highlighted as the query is typed.
func (m *runningModel) handleSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.interrupt()
	case "esc":
		m.editing = false
		m.input.Blur()
		m.setSearch(m.prevSearch)
		return nil
	case "enter":
		m.editing = false
		m.input.Blur()
		if m.search.active() {
			m.jump(-1) // the newest match up to the bottom of the screen
		}
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if q := m.input.Value(); q != m.search.query {
		m.setSearch(newSearch(q))
	}
	return cmd
}

func (m *runningModel) setSearch(s search) {
	m.search, m.matchInfo = s, ""
	for _, v := range []*logView{m.combined, m.single} {
		if v != nil {
			v.current = 0
			v.invalidate()
		}
	}
}

// jump moves to the next (dir > 0, newer) or previous (dir < 0, older) line
// matching the search, wrapping around at either end. Without a current
// match it starts from the edge of the screen.
func (m *runningModel) jump(dir int) {
	if !m.search.active() {
		return
	}
	v := m.view()
	idx := v.matching(m.search)
	v.invalidate()
	if len(idx) == 0 {
		v.current, m.matchInfo = 0, ""
		m.notice = fmt.Sprintf("no matches for %q", m.search.query)
		return
	}
	v.render()
	cur := -1
	if v.current != 0 {
		cur = v.indexOf(v.current)
	}
	var k int // position in idx
	switch {
	case cur < 0 && dir < 0:
		k = sort.SearchInts(idx, v.last+1) - 1
	case cur < 0:
		k = sort.SearchInts(idx, v.first)
	case dir < 0:
		k = sort.SearchInts(idx, cur) - 1
	default:
		k = sort.SearchInts(idx, cur+1)
	}
	switch {
	case k < 0:
		k = len(idx) - 1
		if cur >= 0 {
			m.notice = "search wrapped to the bottom"
		}
	case k >= len(idx):
		k = 0
		if cur >= 0 {
			m.notice = "search wrapped to the top"
		}
	}
	v.current = v.lines[idx[k]].Seq
	v.invalidate()
	v.reveal(idx[k])
	m.matchInfo = fmt.Sprintf("%d/%d", k+1, len(idx))
}

func (m *runningModel) selectedEntry() *sidebarEntry {
	if m.selected < len(m.entries) {
		return m.entries[m.selected]
	}
	return nil
}

func (m *runningModel) restart() {
	if name := m.selectedIdle(); name != "" {
		m.startIdle(name) // restarting what isn't running starts it
		return
	}
	e := m.selectedEntry()
	if e == nil || m.stopping {
		return
	}
	m.report(m.sess.Restart(e.info.Name))
}

// stopOrStart stops the selected project if it is running or waiting to
// run, and starts it (with its dependencies) otherwise.
func (m *runningModel) stopOrStart() {
	if name := m.selectedIdle(); name != "" {
		m.startIdle(name)
		return
	}
	e := m.selectedEntry()
	if e == nil || m.stopping {
		return
	}
	switch {
	case e.info.State == supervisor.Stopping:
	case canStop(e.info.State):
		m.report(m.sess.Stop(e.info.Name))
	default:
		m.report(m.sess.Start(e.info.Name))
	}
}

// canStop reports whether s stops a project in this state (else it starts it).
func canStop(s supervisor.State) bool {
	return s.Alive() || s == supervisor.Waiting
}

func (m *runningModel) report(err error) {
	if err != nil {
		m.notice = err.Error()
	}
}

// interrupt starts a graceful shutdown, or forces it if one is running.
func (m *runningModel) interrupt() tea.Cmd {
	m.confirm, m.toasts = nil, nil // everything stops now
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
	m.selected = max(min(i, m.rows()-1), 0)
	if name := m.selectedName(); m.single != nil && name != "" && name != m.singleFor {
		m.showSingle(name)
	}
	m.layout()
}

// panes reports whether the view is drawn as two panes with a status bar;
// small terminals get a header, rules and a divider instead.
func (m *runningModel) panes() bool {
	return m.width >= paneMinWidth && m.height >= paneMinHeight
}

// sidebarPrefix is the width of the selection marker and the state icon in
// front of a sidebar label: " ▌● " in a pane, " ▌ ● " without.
func (m *runningModel) sidebarPrefix() int {
	if m.panes() {
		return 4
	}
	return 5
}

// logWidth is the width of the log column.
func (m *runningModel) logWidth() int {
	sw := m.sidebarLayout().width
	if m.panes() {
		return m.width - sw - 2 - 3 // the sidebar's borders, "│ " and "│"
	}
	return m.width - sw - 2 // "│ "
}

// sidebarLayout returns the width of the sidebar's content (without borders
// or the divider) and its columns. The sidebar takes at most two fifths of
// the screen. In panes, each process takes two lines (see twoLineLayout);
// in the compact layout one, and when narrow the sparkline is dropped first,
// then the status column, then the git column is narrowed and dropped,
// before names are truncated.
func (m *runningModel) sidebarLayout() sidebarCols {
	base := m.sidebarPrefix() + m.labelWidth + 1 // marker and icon + label + " "
	limit := max(m.width*2/5, 12)
	want, details := 0, 0 // widest git label, widest second line
	for _, e := range m.entries {
		if g, ok := m.git.get(e.info.Name); ok {
			want = max(want, gitWidth(g))
		}
		details = max(details, m.detailsWidth(e))
	}
	status := 0 // widest status
	for _, e := range m.entries {
		if text, _ := statusText(e.info, e.oneShot, m.now()); text != "" {
			status = max(status, len(text), statusWidth)
		}
	}
	if m.panes() {
		c := twoLineLayout(m.sidebarPrefix(), base, limit, details, status)
		c.width = max(c.width, min(m.idleRowWidth(), limit))
		return c
	}
	// Column sets from most to least complete.
	for _, o := range []struct{ spark, status, git bool }{
		{true, true, true}, {false, true, true}, {false, false, true},
		{true, true, false}, {false, true, false},
	} {
		c, names := sidebarCols{}, base
		if o.spark {
			c.spark = sparkCells
			names += sparkCells + 1
		}
		if o.status && status > 0 {
			c.status = status
			names += status + 1
		}
		if o.git {
			c.git = min(want, maxGitWidth, limit-names-1)
			if c.git <= 0 || c.git < min(want, minGitWidth) {
				continue
			}
			c.width = names + c.git + 1
			return c
		}
		if names <= limit {
			c.width = names
			return c
		}
	}
	return sidebarCols{width: max(min(base, limit), 12)}
}

// twoLineLayout lays out a sidebar whose processes take two lines: the
// name, status column and sparkline, then the details (ports and git label)
// under the name. When narrow, line 1 drops the sparkline first, then the
// status column, before names are truncated; the details are truncated to
// fit. The sidebar grows to fit the widest details (see detailsWidth).
func twoLineLayout(prefix, base, limit, details, status int) sidebarCols {
	c := sidebarCols{twoLine: true}
	for _, o := range []struct{ spark, status bool }{{true, true}, {false, true}, {false, false}} {
		c.width, c.spark, c.status = base, 0, 0
		if o.spark {
			c.spark = sparkCells
			c.width += sparkCells + 1
		}
		if o.status && status > 0 {
			c.status = status
			c.width += status + 1
		}
		if c.width <= limit {
			break
		}
	}
	if details > 0 {
		c.width = max(c.width, prefix+details+1)
	}
	c.width = max(min(c.width, limit), 12)
	return c
}

// idleRowWidth is the width the rows of the projects not started want in
// the two-line layout: their names, then their git labels aligned after
// them. 0 when there are none.
func (m *runningModel) idleRowWidth() int {
	if len(m.idle) == 0 {
		return 0
	}
	git := 0
	for _, n := range m.idle {
		if g, ok := m.git.get(n); ok {
			git = max(git, gitWidth(g))
		}
	}
	w := m.sidebarPrefix() + m.idleWidth + 1
	if git > 0 {
		w += min(git, maxGitWidth) + 1
	}
	return w
}

// sidebarCols is the sidebar's layout: its width without borders or the
// divider, and the widths of its status, sparkline and git columns, 0 if
// hidden. With twoLine, each process takes two lines and git is unused: the
// git label goes on the second line.
type sidebarCols struct {
	width, status, spark, git int
	twoLine                   bool
}

// entryLines is the number of sidebar lines each of the session's
// processes takes: two in panes (see twoLineLayout), one in the compact
// layout.
func (m *runningModel) entryLines() int {
	if m.panes() {
		return 2
	}
	return 1
}

// statusText returns what the status column shows for a process: its
// uptime while it runs, why it failed (failed is set), "done" for a one-shot
// task that finished, or nothing.
func statusText(info supervisor.ProcessInfo, oneShot bool, now time.Time) (text string, failed bool) {
	switch info.State {
	case supervisor.Exited:
		if oneShot {
			return "done", false
		}
	case supervisor.Crashed:
		if info.Exit.Code > 0 {
			return "exit " + strconv.Itoa(info.Exit.Code), true
		}
		return "crashed", true
	case supervisor.Blocked:
		return "blocked", true
	case supervisor.Starting, supervisor.Running, supervisor.Stopping:
		if !info.StartedAt.IsZero() {
			return uptime(now.Sub(info.StartedAt)), false
		}
	}
	return "", false
}

// uptime formats a duration in at most two units: 45s, 4m02s, 1h12m, 3d04h.
func uptime(d time.Duration) string {
	s := int(max(d, 0) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case s < 86400:
		return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
	default:
		return fmt.Sprintf("%dd%02dh", s/86400, s%86400/3600)
	}
}

// bodyHeight is the number of log rows: all but the header (or top border),
// the rule (bottom border), the status bar (rule) and the help line.
func (m *runningModel) bodyHeight() int {
	return max(m.height-4, 0)
}

// layout updates sizes and scroll positions after a change.
func (m *runningModel) layout() {
	h := m.bodyHeight()
	m.combined.setSize(m.logWidth(), h)
	if m.single != nil {
		m.single.setSize(m.logWidth(), h)
	}
	// The session's projects scroll in the rows the ones not started leave.
	idle := m.idleRows(h)
	sh, ih := (h-idle)/m.entryLines(), max(idle-1, 0)
	switch sel := m.selected; {
	case sel >= len(m.entries):
	case sel < m.sideTop:
		m.sideTop = sel
	case sh > 0 && sel >= m.sideTop+sh:
		m.sideTop = sel - sh + 1
	}
	m.sideTop = max(min(m.sideTop, len(m.entries)-sh), 0)
	switch sel := m.selected - len(m.entries); {
	case sel < 0:
	case sel < m.idleTop:
		m.idleTop = sel
	case ih > 0 && sel >= m.idleTop+ih:
		m.idleTop = sel - ih + 1
	}
	m.idleTop = max(min(m.idleTop, len(m.idle)-ih), 0)
}

// formatCombined renders a line with its project's label in front.
func (m *runningModel) formatCombined(line supervisor.LogLine, width int) []string {
	e := m.entry(line.Project)
	lw := min(m.labelWidth, maxPrefixWidth)
	rows := wrap(m.display(line, m.combined.current), width-lw-1)
	label := e.style.Render(pad(ansi.Truncate(e.label, lw, "…"), lw)) + " "
	indent := strings.Repeat(" ", lw+1)
	for i, row := range rows {
		if i == 0 {
			rows[i] = label + row
		} else {
			rows[i] = indent + row
		}
	}
	return rows
}

// display returns a line's text sanitized, with runr's own lines faint and
// search matches highlighted, ready to wrap.
func (m *runningModel) display(line supervisor.LogLine, current uint64) string {
	text := sanitize(line.Text)
	switch line.Stream {
	case supervisor.System:
		text = m.st.system + text
	case supervisor.SystemError:
		text = m.st.systemErr + text
	}
	if m.search.active() {
		if ranges := m.search.find(plain(text)); len(ranges) > 0 {
			on := m.st.match
			if current != 0 && line.Seq == current {
				on = m.st.current
			}
			text = highlight(text, ranges, on)
		}
	}
	return text
}

// formatSingle renders a line of the single-process view.
func (m *runningModel) formatSingle(line supervisor.LogLine, width int) []string {
	return wrap(m.display(line, m.single.current), width)
}

func (m *runningModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "runr · " + strings.Join(m.titles, " + ")
	v.ForegroundColor, v.BackgroundColor = m.st.screen()
	return v
}

func (m *runningModel) render() string {
	if m.width == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return "terminal too small"
	}
	cols := m.sidebarLayout()
	var logRows []string
	if m.picker != nil {
		logRows = m.picker.view(m.logWidth()-1, m.bodyHeight()) // a space before the border
	} else {
		logRows = m.drawToasts(m.view().view(), m.logWidth())
	}
	if m.panes() {
		return m.renderPanes(cols, logRows)
	}

	sw := cols.width
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteByte('\n')
	b.WriteString(m.st.rule.Render(strings.Repeat("─", sw) + "┬" + strings.Repeat("─", m.width-sw-1)))
	b.WriteByte('\n')
	divider := m.st.rule.Render("│")
	for r, side := range m.sidebarRows(m.bodyHeight(), cols) {
		b.WriteString(side)
		b.WriteString(divider)
		b.WriteByte(' ')
		b.WriteString(logRows[r])
		b.WriteByte('\n')
	}
	b.WriteString(m.st.rule.Render(strings.Repeat("─", sw) + "┴" + strings.Repeat("─", m.width-sw-1)))
	b.WriteByte('\n')
	b.WriteString(m.help())
	return b.String()
}

func (m *runningModel) header() string {
	left := " " + m.st.badge + "  " + m.st.bold.Render(strings.Join(m.titles, " + "))
	name := "combined"
	if m.single != nil {
		name = m.styleOf(m.singleFor).Render(m.labelOf(m.singleFor))
	}
	var right string
	switch v := m.view(); {
	case m.forced:
		right = m.st.warn.Render("killing…")
	case m.stopping:
		right = m.st.warn.Render(fmt.Sprintf("stopping… %d left · ctrl+c to force", m.alive()))
	case v.following():
		right = name + m.st.faint.Render(" · following")
	default:
		right = name + " · " + m.st.warn.Render("paused")
		if n := v.below(); n > 0 {
			right += m.st.faint.Render(fmt.Sprintf(" · %d more below", n))
		}
	}
	right += " "
	states := make([]supervisor.State, len(m.entries))
	for i, e := range m.entries {
		states[i] = e.info.State
	}
	// The counts go first when narrow, then the right side.
	if counts := m.st.stateCounts(states, "  ", spinner(m.now())); counts != "" {
		if line, ok := spread(left, counts+"   "+right, m.width); ok {
			return line
		}
	}
	if line, ok := spread(left, right, m.width); ok {
		return line
	}
	return ansi.Truncate(left, m.width, "…")
}

// spread puts left and right at the edges of a line of width cells, if both
// fit with a gap between them.
func spread(left, right string, width int) (string, bool) {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return "", false
	}
	return left + strings.Repeat(" ", gap) + right, true
}

// renderPanes draws the processes and logs panes, the status bar and the
// help line.
func (m *runningModel) renderPanes(cols sidebarCols, logRows []string) string {
	sw := cols.width
	side, sideTitle := m.st.accent, m.st.accent.Bold(true).Render("processes")
	title, note, border := m.logPane()
	logW := m.width - sw - 2
	sideRows := m.sidebarRows(m.bodyHeight(), cols)
	if m.picker != nil {
		// The picker has the focus: the processes pane fades.
		side, sideTitle = m.st.faint, m.st.faint.Render("processes")
		for i, row := range sideRows {
			sideRows[i] = m.st.faint.Render(ansi.Strip(row))
		}
	}

	var b strings.Builder
	b.WriteString(paneTop(sw+2, sideTitle, "", side))
	b.WriteString(paneTop(logW, title, note, border))
	b.WriteByte('\n')
	for r, row := range sideRows {
		b.WriteString(side.Render("│"))
		b.WriteString(row)
		b.WriteString(side.Render("│"))
		b.WriteString(border.Render("│"))
		b.WriteByte(' ')
		b.WriteString(pad(logRows[r], logW-3))
		b.WriteString(border.Render("│"))
		b.WriteByte('\n')
	}
	b.WriteString(paneBottom(sw+2, side))
	b.WriteString(paneBottom(logW, border))
	b.WriteByte('\n')
	b.WriteString(m.statusBar())
	b.WriteByte('\n')
	b.WriteString(m.help())
	return b.String()
}

// logPane returns the log pane's title, the note on the right of its top
// border, and its border style: dim while following, yellow while paused,
// accent while searching or picking.
func (m *runningModel) logPane() (title, note string, border lipgloss.Style) {
	v := m.view()
	border = m.st.faint
	switch {
	case m.picker != nil || m.editing || m.search.active():
		border = m.st.accent
	case !v.following():
		border = m.st.warn
	}
	if m.picker != nil {
		return m.picker.titleText(), "", border
	}

	title = m.st.bold.Render("logs") + m.st.faint.Render(" · combined")
	if m.single != nil {
		title = m.st.bold.Render("logs") + m.st.faint.Render(" · ") + m.styleOf(m.singleFor).Render(m.labelOf(m.singleFor))
	}
	switch {
	case m.forced:
		note = m.st.warn.Render("killing…")
	case m.stopping:
		note = m.st.warn.Render(fmt.Sprintf("stopping… %d left · ctrl+c to force", m.alive()))
	case v.following():
		note = m.st.faint.Render("following")
	default:
		note = m.st.warn.Render("paused")
		if n := v.below(); n > 0 {
			note += m.st.faint.Render(fmt.Sprintf(" · %d more below", n))
		}
	}
	return title, note, border
}

// statusBar renders the mode, the session with its state counts, and the
// selected project with its git state.
func (m *runningModel) statusBar() string {
	b := statusBar{mode: "FOLLOW", color: m.st.pal.accent, title: strings.Join(m.titles, " + "), clock: m.now().Format("15:04")}
	switch {
	case m.stopping:
		b.mode, b.color = "STOPPING", m.st.pal.ansi[ansiRed]
	case m.picker != nil:
		b.mode, b.color = m.picker.modeName(), m.st.pal.ansi[ansiBlue]
	case m.confirm != nil:
		b.mode, b.color = strings.ToUpper(m.confirm.action), m.st.pal.ansi[ansiBlue]
	case m.editing || m.search.active():
		b.mode, b.color = "SEARCH", m.st.pal.ansi[ansiMagenta]
	case !m.view().following():
		b.mode, b.color = "PAUSED", m.st.pal.ansi[ansiYellow]
	}
	states := make([]supervisor.State, len(m.entries))
	for i, e := range m.entries {
		states[i] = e.info.State
	}
	b.info = m.st.stateCounts(states, " ", spinner(m.now()))
	if name := m.selectedName(); name != "" {
		b.project = m.styleOf(name).Bold(true).Render(m.labelOf(name))
		if g, ok := m.git.get(name); ok {
			b.project += m.st.faint.Render(" · ") + gitLabel(m.st, g, gitWidth(g))
		}
	}
	return m.st.statusBar(b, m.width)
}

// alive counts the processes that still have to exit.
func (m *runningModel) alive() int {
	n := 0
	for _, e := range m.entries {
		if e.info.State.Alive() {
			n++
		}
	}
	return n
}

// sidebarRow renders a process of the session: one line in the compact
// layout, two in panes (see twoLineLayout).
func (m *runningModel) sidebarRow(i int, cols sidebarCols) []string {
	full, width := cols.width, cols.width
	if i >= len(m.entries) {
		return []string{strings.Repeat(" ", width)}
	}
	if cols.git > 0 {
		width -= cols.git + 1 // the git column and the margin after it
	}
	e := m.entries[i]
	selected := i == m.selected
	marker := " "
	style := e.style
	if selected {
		marker = e.style.Render("▌")
		style = style.Bold(true)
		if m.st.selBar == "" {
			style = style.Reverse(true) // no background tint: highlight the name
		}
	}
	labelWidth := width - m.sidebarPrefix() - 1 // marker and icon, and the right margin
	if cols.status > 0 {
		labelWidth -= cols.status + 1
	}
	if cols.spark > 0 {
		labelWidth -= cols.spark + 1
	}
	label := ansi.Truncate(e.label, labelWidth, "…")
	icon := m.st.stateIcon(e.info.State, spinner(m.now()))
	row := " " + marker + " " + icon + " " + style.Render(label)
	if m.panes() {
		row = " " + marker + icon + " " + style.Render(label)
	}
	row = pad(row, m.sidebarPrefix()+labelWidth)
	if cols.status > 0 {
		text, failed := statusText(e.info, e.oneShot, m.now())
		row += " " + strings.Repeat(" ", cols.status-len(text))
		if failed {
			row += m.st.err.Render(text)
		} else {
			row += m.st.faint.Render(text)
		}
	}
	if cols.spark > 0 {
		style := e.style
		if e.info.State == supervisor.Crashed {
			style = m.st.err
		}
		row += " " + m.st.sparkline(&e.rate, m.now().Unix(), style)
	}
	if cols.git > 0 {
		row = pad(row, width)
		if g, ok := m.git.get(e.info.Name); ok {
			row += gitLabel(m.st, g, cols.git)
		}
	}
	rows := []string{pad(row, full)}
	if cols.twoLine {
		rows = append(rows, pad(" "+marker+"  "+m.details(e, full-m.sidebarPrefix()-1), full))
	}
	if selected && m.st.selBar != "" {
		for i, r := range rows {
			rows[i] = onBackground(r, m.st.selBar)
		}
	}
	return rows
}

// details renders the second line of a process in the sidebar, in at most
// width cells: its ports and git label joined by " · ", faint except for
// the git markers. The git label is truncated first.
func (m *runningModel) details(e *sidebarEntry, width int) string {
	if width <= 0 {
		return ""
	}
	out := ""
	if e.info.State.Alive() {
		out = ansi.Truncate(portsLabel(m.ports.get(e.info.Name)), width, "…")
	}
	g, ok := m.git.get(e.info.Name)
	if !ok {
		return m.st.faint.Render(out)
	}
	if out == "" {
		return gitLabelStyled(m.st, g, width, m.st.faint)
	}
	if rest := width - ansi.StringWidth(out) - 3; rest >= 2 { // room for at least "x…"
		return m.st.faint.Render(out+" · ") + gitLabelStyled(m.st, g, rest, m.st.faint)
	}
	return m.st.faint.Render(out)
}

// detailsWidth is the width the second line of a process wants (see
// details), with the git label at most maxGitWidth wide.
func (m *runningModel) detailsWidth(e *sidebarEntry) int {
	w := 0
	if e.info.State.Alive() {
		w = ansi.StringWidth(portsLabel(m.ports.get(e.info.Name)))
	}
	if g, ok := m.git.get(e.info.Name); ok {
		if w > 0 {
			w += 3 // " · "
		}
		w += min(gitWidth(g), maxGitWidth)
	}
	return w
}

func (m *runningModel) help() string {
	if m.confirm != nil {
		return questionLine(m.st, m.confirm, m.width)
	}
	if m.notice != "" {
		return ansi.Truncate(" "+m.st.warn.Render(m.notice), m.width, "…")
	}
	if m.picker != nil {
		return ansi.Truncate(" "+m.st.hintList([][2]string{
			{"type", "to filter"}, {"↑↓", "select"}, {"enter", m.picker.action}, {"esc", "cancel"},
		}), m.width, "…")
	}
	if m.editing {
		hints := m.st.hintList([][2]string{{"enter", "search"}, {"esc", "cancel"}})
		m.input.SetWidth(max(m.width-ansi.StringWidth(hints)-6, 10)) // " /", the cursor and the gap
		return ansi.Truncate(" "+m.st.bold.Render("/")+m.input.View()+"  "+hints, m.width, "…")
	}
	view, stop := "view", "start"
	if m.single != nil {
		view = "all"
	}
	if e := m.selectedEntry(); e != nil && canStop(e.info.State) {
		stop = "stop"
	}
	// Hints are dropped from the end of the middle when narrow.
	keys := [][2]string{{"↑↓", "select"}, {"tab", view}, {"r", "restart"}, {"s", stop}, {"+", "start"}}
	if m.selectedIdle() != "" {
		keys = [][2]string{{"↑↓", "select"}, {"tab", view}, {"s", "start"}, {"+", "start"}}
	}
	if m.showAll {
		keys[len(keys)-1][1] = "group" // projects start from the sidebar
	}
	if e := m.selectedEntry(); e != nil && m.selectedIdle() == "" && e.info.State.Alive() && len(m.ports.get(e.info.Name)) > 0 {
		keys = append(keys, [2]string{"o", "open"})
	}
	if name := m.selectedName(); name != "" {
		if _, ok := m.git.get(name); ok {
			keys = append(keys, [2]string{"b", "branch"}, [2]string{"p", "pull"})
		}
	}
	keys = append(keys, [][2]string{
		{"/", "search"}, {"pgup/pgdn", "scroll"}, {"g/G", "top/bottom"}, {"f", "follow"}, {"w", "save"},
	}...)
	if len(m.idle) > 0 || !m.showAll && len(m.entries) < len(m.cfg.Projects) {
		all := "hide others"
		if !m.showAll {
			all = "show all"
		}
		keys = append(keys, [2]string{"a", all})
	}
	keys = append(keys, [2]string{"q", "quit"})
	var parts []string
	if m.search.active() {
		keys = append([][2]string{{"n/N", "next/prev"}, {"esc", "clear"}}, keys...)
		parts = append(parts, m.st.bold.Render("/"+m.search.query)+" "+m.st.faint.Render(m.matchInfo))
	}
	if m.stopping {
		keys, parts = [][2]string{{"ctrl+c", "force quit"}}, nil
	}
	for _, k := range keys {
		parts = append(parts, m.st.hint(k[0], k[1]))
	}
	return m.st.fitHints(parts, m.width)
}

// pad pads s with spaces to width cells.
func pad(s string, width int) string {
	if n := width - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
