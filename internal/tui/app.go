package tui

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/config"
	runrstate "github.com/Skaanning/runr/internal/state" // "state" is a test helper in this package
)

// StartFunc validates and starts the given groups and projects, with extra
// arguments by project (see Session.SetArgs), returning the running session.
type StartFunc func(names []string, args map[string]string) (Session, error)

// appModel shows the launcher until something is started or attached to,
// then the running view in the same program. Detaching from it (d), or the
// session ending elsewhere, returns to the launcher.
type appModel struct {
	cfg      *config.Config
	start    StartFunc
	bg       Background // nil: no running sessions are listed
	launcher *launcherModel
	running  *runningModel
	size     tea.WindowSizeMsg
	detached []int // sessions detached from (d), by pid
}

func (a *appModel) Init() tea.Cmd {
	if a.running != nil { // a session runr up or runr attach showed first
		return a.running.Init()
	}
	return a.launcher.Init()
}

func (a *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		a.size = size
	}
	if _, ok := msg.(hangupMsg); ok {
		return a, a.hangup()
	}
	if a.running != nil {
		if cmd, ok := a.launcher.bg.update(msg); ok { // paused meanwhile
			return a, cmd
		}
		_, cmd := a.running.Update(msg)
		if a.running.detached || a.running.lost != nil {
			return a, a.backToLauncher()
		}
		return a, cmd
	}
	switch msg := msg.(type) {
	case startMsg:
		sess, err := a.start(msg.names, msg.args)
		if err != nil {
			// Multi-line errors (e.g. several missing dirs) fit on one line.
			a.launcher.notice = oneLine(err.Error())
			return a, nil
		}
		return a, a.show(sess, a.cfg, msg.names, msg.notice)
	case attachMsg:
		att, err := a.bg.Attach(msg.pid)
		if err != nil {
			a.launcher.notice = "can't attach: " + oneLine(err.Error())
			return a, a.launcher.bg.refresh()
		}
		return a, a.show(att.Session, att.Config, att.Titles, "")
	}
	_, cmd := a.launcher.Update(msg)
	return a, cmd
}

// hangup handles the terminal going away (SIGHUP, SIGTERM): a session in
// another process only loses this view, whether it goes on is up to it;
// one in this process stops, as with Ctrl+C.
func (a *appModel) hangup() tea.Cmd {
	if a.running == nil {
		return tea.Quit
	}
	if d, ok := a.running.sess.(dropper); ok {
		d.Drop(errors.New("the view's terminal closed"))
		return tea.Quit
	}
	_, cmd := a.running.Update(interruptMsg{})
	return cmd
}

// dropper is a session in another process whose view can go away without
// detaching (control.Remote).
type dropper interface {
	Drop(reason error)
}

// pider is a session that knows its pid (control.Remote).
type pider interface {
	Pid() int
}

func (a *appModel) View() tea.View {
	if a.running != nil {
		return a.running.View()
	}
	return a.launcher.View()
}

// show opens the running view of sess.
func (a *appModel) show(sess Session, cfg *config.Config, names []string, notice string) tea.Cmd {
	a.running = newRunningModel(sess, cfg, names, a.launcher.st)
	a.running.git = a.launcher.git // already has the git state
	a.running.notice = notice
	if a.size.Width > 0 {
		a.running.Update(a.size)
	}
	a.launcher.bg.pause()
	return a.running.Init()
}

// backToLauncher closes the running view, which was detached from or whose
// session ended, and shows the launcher again with its list refreshed.
func (a *appModel) backToLauncher() tea.Cmd {
	m := a.running
	a.running = nil
	if c, ok := m.sess.(interface{ Close() }); ok {
		c.Close()
	}
	l := a.launcher
	l.notice = "detached: the session goes on running"
	if m.lost != nil {
		l.notice = oneLine(m.lost.Error())
	}
	if p, ok := m.sess.(pider); ok && m.detached {
		a.detached = append(a.detached, p.Pid())
		l.preselectSession(p.Pid())
	}
	l.gitStale = true // the running view refreshed only its own projects
	l.Update(a.size)
	return tea.Batch(l.bg.resume(), clockTick(l, l.now()), tea.RequestBackgroundColor)
}

// LauncherSetup is what RunLauncher shows.
type LauncherSetup struct {
	Config *config.Config
	Start  StartFunc
	Save   SaveFunc  // writes changes made in the forms; nil: kept in memory
	Git    GitReader // reads the projects' git state; nil hides it
	Last   *runrstate.LastSession
	// Left, if set, are processes a killed runr left: the launcher (or the
	// first view, which then starts its titles) asks whether to kill them.
	Left       *Leftovers
	Background Background // lists the sessions running elsewhere; nil: none
	// First, if set, is shown before the launcher: the running view of a
	// session runr up started or runr attach attached to.
	First *Attached
}

// LauncherResult is how RunLauncher ended.
type LauncherResult struct {
	Shown    Session // the running view's session when the user quit; nil: quit in the launcher
	Detached []int   // the sessions detached from (d) meanwhile, by pid
}

// RunLauncher shows the launcher, and the running view once the user starts
// something, until the user quits; with s.First, its running view first.
// d in a running view returns to the launcher. Signals on sigs are handled
// like Ctrl+C; on hangups (the terminal closed) a view of a session in
// another process just goes away, as does runr.
//
// The error is what Run returns about how the last running view ended.
func RunLauncher(s LauncherSetup, sigs, hangups <-chan os.Signal) (LauncherResult, error) {
	a := newAppModel(s, settingsStyles(s.Config.Settings))
	err := run(a, sigs, hangups)
	res := LauncherResult{Detached: a.detached}
	if a.running == nil {
		return res, err
	}
	res.Shown = a.running.sess
	if err != nil {
		return res, err
	}
	return res, a.running.ended()
}

// newAppModel returns the app RunLauncher runs.
func newAppModel(s LauncherSetup, st *styles) *appModel {
	launcher := newLauncherModel(s.Config, s.Save, st)
	launcher.git = newGitTracker(s.Git)
	launcher.bg = newBackgroundTracker(s.Background)
	launcher.setLast(s.Last)
	a := &appModel{cfg: s.Config, start: s.Start, bg: s.Background, launcher: launcher}
	switch {
	case s.First != nil:
		a.show(s.First.Session, s.First.Config, s.First.Titles, "")
		if s.Left != nil {
			a.running.askLeftovers(s.Left)
		}
	case s.Left != nil:
		launcher.askLeftovers(s.Left)
	}
	return a
}

// Run shows the running view until the user quits and every process has
// stopped. names are the groups and projects of the session, shown in the
// header. git reads the projects' git state; nil hides it. Signals received
// on sigs are handled like Ctrl+C.
//
// If left is set, sess has not been started yet: the view first asks
// whether to kill those processes, and then starts names. Run returns
// ErrDetached when the user left the session running (d, for a Detacher),
// and the session's Lost error when it ended without the view stopping it.
func Run(sess Session, cfg *config.Config, names []string, git GitReader, left *Leftovers, sigs <-chan os.Signal) error {
	m := newRunningModel(sess, cfg, names, settingsStyles(cfg.Settings))
	m.git = newGitTracker(git)
	if left != nil {
		m.askLeftovers(left)
	}
	if err := run(m, sigs, nil); err != nil {
		return err
	}
	return m.ended()
}

// ended is how the running view ended: ErrDetached when the user left the
// session running, the session's Lost error when it ended on its own, nil
// when the user stopped it.
func (m *runningModel) ended() error {
	switch {
	case m.detached:
		return ErrDetached
	case m.lost != nil:
		return m.lost
	}
	return nil
}

// settingsStyles returns the styles for the theme settings.
func settingsStyles(s config.Settings) *styles {
	st := newStyles(themeNamed(s.Theme()))
	st.paint = s.ThemeBackground()
	return st
}

// run runs model until it quits. Signals on sigs arrive as interruptMsg,
// on hangups as hangupMsg.
func run(model tea.Model, sigs, hangups <-chan os.Signal) error {
	p := tea.NewProgram(model, tea.WithoutSignalHandler())
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-sigs:
				p.Send(interruptMsg{})
			case <-hangups:
				p.Send(hangupMsg{})
			case <-done:
				return
			}
		}
	}()
	_, err := p.Run()
	return err
}
