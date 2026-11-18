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

// appModel shows the launcher until something is started, then the running
// view in the same program.
type appModel struct {
	cfg      *config.Config
	start    StartFunc
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
		_, cmd := a.running.Update(msg)
		return a, cmd
	}
	if msg, ok := msg.(startMsg); ok {
		sess, err := a.start(msg.names, msg.args)
		if err != nil {
			// Multi-line errors (e.g. several missing dirs) fit on one line.
			a.launcher.notice = oneLine(err.Error())
			return a, nil
		}
		a.running = newRunningModel(sess, a.cfg, msg.names, a.launcher.st)
		a.running.git = a.launcher.git // already has the git state
		a.running.notice = msg.notice
		a.running.Update(a.size)
		return a, a.running.Init()
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

// RunLauncher shows the launcher, and the running view once the user starts
// something, until the user quits. Changes made in the launcher's forms are
// written with save. git reads the projects' git state; nil hides it. last,
// if set, is the last session, which l starts again. If left is set, the
// launcher first asks whether to kill those processes. Signals received on
// sigs are handled like Ctrl+C. It returns what Run does about how the
// running view ended.
func RunLauncher(cfg *config.Config, start StartFunc, save SaveFunc, git GitReader, last *runrstate.LastSession, left *Leftovers, sigs <-chan os.Signal) error {
	launcher := newLauncherModel(cfg, save, settingsStyles(cfg.Settings))
	launcher.git = newGitTracker(git)
	launcher.setLast(last)
	if left != nil {
		launcher.askLeftovers(left)
	}
	a := &appModel{cfg: cfg, start: start, launcher: launcher}
	if err := run(a, sigs); err != nil {
		return err
	}
	if a.running != nil {
		return a.running.ended()
	}
	return nil
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
