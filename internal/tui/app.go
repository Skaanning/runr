package tui

import (
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
}

func (a *appModel) Init() tea.Cmd { return a.launcher.Init() }

func (a *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		a.size = size
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
	a.running.Update(a.size)
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
	l.Update(a.size)
	return tea.Batch(l.bg.resume(), clockTick(l, l.now()))
}

// RunLauncher shows the launcher, and the running view once the user starts
// something, until the user quits. Changes made in the launcher's forms are
// written with save. git reads the projects' git state; nil hides it. last,
// if set, is the last session, which l starts again. If left is set, the
// launcher first asks whether to kill those processes. bg, if set, lists
// the sessions running elsewhere, to attach to and stop. Signals received
// on sigs are handled like Ctrl+C.
//
// It returns the session the running view showed when the user quit (nil
// if that was in the launcher), and what Run does about how the view ended.
func RunLauncher(cfg *config.Config, start StartFunc, save SaveFunc, git GitReader, last *runrstate.LastSession, left *Leftovers, bg Background, sigs <-chan os.Signal) (Session, error) {
	launcher := newLauncherModel(cfg, save, settingsStyles(cfg.Settings))
	launcher.git = newGitTracker(git)
	launcher.bg = newBackgroundTracker(bg)
	launcher.setLast(last)
	if left != nil {
		launcher.askLeftovers(left)
	}
	a := &appModel{cfg: cfg, start: start, bg: bg, launcher: launcher}
	err := run(a, sigs)
	if a.running == nil {
		return nil, err
	}
	if err != nil {
		return a.running.sess, err
	}
	return a.running.sess, a.running.ended()
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
	if err := run(m, sigs); err != nil {
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

func run(model tea.Model, sigs <-chan os.Signal) error {
	p := tea.NewProgram(model, tea.WithoutSignalHandler())
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
