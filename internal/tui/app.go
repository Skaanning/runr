package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"

	"runr/internal/config"
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

func (a *appModel) Init() tea.Cmd { return nil }

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
		a.running = newRunningModel(sess, a.cfg, msg.names)
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
// written with save. Signals received on sigs are handled like Ctrl+C.
func RunLauncher(cfg *config.Config, start StartFunc, save SaveFunc, sigs <-chan os.Signal) error {
	return run(&appModel{cfg: cfg, start: start, launcher: newLauncherModel(cfg, save)}, sigs)
}

// Run shows the running view until the user quits and every process has
// stopped. names are the groups and projects that were started, shown in
// the header. Signals received on sigs are handled like Ctrl+C.
func Run(sess Session, cfg *config.Config, names []string, sigs <-chan os.Signal) error {
	return run(newRunningModel(sess, cfg, names), sigs)
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
