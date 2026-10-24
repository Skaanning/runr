package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/config"
)

// StartFunc validates and starts the given groups and projects, returning
// the running session.
type StartFunc func(names []string) (Session, error)

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
		sess, err := a.start(msg.names)
		if err != nil {
			// Multi-line errors (e.g. several missing dirs) fit on one line.
			a.launcher.notice = oneLine(err.Error())
			return a, nil
		}
		a.running = newRunningModel(sess, a.cfg, msg.names, a.launcher.st)
		a.running.git = a.launcher.git // already has the git state
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
// written with save. git reads the projects' git state; nil hides it.
// Signals received on sigs are handled like Ctrl+C.
func RunLauncher(cfg *config.Config, start StartFunc, save SaveFunc, git GitReader, sigs <-chan os.Signal) error {
	launcher := newLauncherModel(cfg, save, newStyles(terminalPalette))
	launcher.git = newGitTracker(git)
	return run(&appModel{cfg: cfg, start: start, launcher: launcher}, sigs)
}

// Run shows the running view until the user quits and every process has
// stopped. names are the groups and projects that were started, shown in
// the header. git reads the projects' git state; nil hides it. Signals
// received on sigs are handled like Ctrl+C.
func Run(sess Session, cfg *config.Config, names []string, git GitReader, sigs <-chan os.Signal) error {
	m := newRunningModel(sess, cfg, names, newStyles(terminalPalette))
	m.git = newGitTracker(git)
	return run(m, sigs)
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
