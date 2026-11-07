package tui

import (
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	runrstate "github.com/Skaanning/runr/internal/state"
	"github.com/Skaanning/runr/internal/supervisor"
)

func TestLauncherArgsForm(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(key("R")) // on backend: db, then api, which needs it
	if m.form == nil {
		t.Fatal("R opened no form")
	}
	screen := strings.Join(launcherScreen(m), "\n")
	for _, want := range []string{"start with arguments", "ctrl+s start", "db", "go run . …", "api", "go run ./cmd/server …", "Start", "START WITH ARGUMENTS"} {
		if !strings.Contains(screen, want) {
			t.Errorf("no %q in:\n%s", want, screen)
		}
	}
	if fields := m.form.form.fields; len(fields) != 2 || fields[0].title != "db" || fields[1].title != "api" {
		t.Fatalf("fields = %v", fields)
	}

	m.Update(key("tab"))
	m.Update(tea.PasteMsg{Content: "--verbose "})
	if help := launcherScreen(m)[23]; !strings.Contains(help, "enter  start") {
		t.Errorf("help on the last field = %q", help)
	}
	_, cmd := m.Update(key("ctrl+s"))
	if m.form != nil || cmd == nil {
		t.Fatalf("form = %v, cmd = %v", m.form, cmd)
	}
	msg := cmd().(startMsg)
	if !slices.Equal(msg.names, []string{"backend"}) || !maps.Equal(msg.args, map[string]string{"db": "", "api": "--verbose"}) {
		t.Fatalf("start = %q %q", msg.names, msg.args)
	}

	// Pre-filled with the last arguments; enter in the last field starts.
	m.Update(key("shift+enter"))
	if m.form == nil || m.form.form.fields[1].input.Value() != "--verbose" {
		t.Fatal("shift+enter: no form pre-filled with the last arguments")
	}
	m.Update(key("enter")) // db → api
	_, cmd = m.Update(key("enter"))
	if cmd == nil || m.form != nil {
		t.Fatal("enter in the last field did not start")
	}
	if msg := cmd().(startMsg); msg.args["api"] != "--verbose" {
		t.Errorf("args = %q", msg.args)
	}

	m.Update(key("R"))
	if _, cmd := m.Update(key("esc")); cmd != nil || m.form != nil {
		t.Error("esc did not cancel")
	}

	m.gitAction = "git pull" // the start is refused; the arguments are kept
	m.Update(key("R"))
	if _, cmd := m.Update(key("ctrl+s")); cmd != nil || m.notice != "git pull is still running" {
		t.Errorf("cmd = %v, notice = %q", cmd, m.notice)
	}
}

func TestKeyShiftEnter(t *testing.T) {
	if got := (tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}).String(); got != "shift+enter" {
		t.Fatalf("shift+enter is %q", got)
	}
}

func TestAppPassesArgs(t *testing.T) {
	cfg := launcherConfig(t)
	var got map[string]string
	app := &appModel{cfg: cfg, launcher: newLauncherModel(cfg, nil, newStyles(terminalTheme)), start: func(names []string, args map[string]string) (Session, error) {
		got = args
		return newFakeSession("api"), nil
	}}
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	app.Update(startMsg{names: []string{"api"}, args: map[string]string{"api": "--x"}})
	if got["api"] != "--x" {
		t.Fatalf("args = %q", got)
	}
}

func TestRunningArgs(t *testing.T) {
	m, sess := newTestModel(t, 120, 20)
	m.Update(eventsMsg{events: []supervisor.Event{
		supervisor.StateEvent{Info: supervisor.ProcessInfo{Name: "db", State: supervisor.Running, Args: "--old"}},
	}})

	m.Update(key("tab")) // db's own log: its arguments in the title
	if got := screen(m)[0]; !strings.Contains(got, "─ logs · db --old ─") {
		t.Errorf("title = %q", got)
	}
	if !strings.Contains(m.handleKeyNotice(t, "c"), m.cfg.Projects["db"].Cmd+" --old") {
		t.Errorf("copied without the arguments: %q", m.notice)
	}

	m.Update(key("R")) // db is selected
	if m.argsFor != "db" || m.input.Value() != "--old" {
		t.Fatalf("argsFor = %q, input = %q", m.argsFor, m.input.Value())
	}
	if got := helpRow(m); !strings.Contains(got, "db args --old") || !strings.Contains(got, "enter  restart") {
		t.Errorf("help = %q", got)
	}
	m.Update(tea.PasteMsg{Content: " --new"})
	m.Update(key("enter"))
	if m.argsFor != "" || !slices.Equal(sess.calls, []string{"args db --old --new", "restart db"}) {
		t.Fatalf("argsFor = %q, calls = %q", m.argsFor, sess.calls)
	}

	sess.calls = nil
	m.Update(key("R"))
	m.Update(key("esc"))
	if m.argsFor != "" || len(sess.calls) != 0 {
		t.Errorf("esc: argsFor = %q, calls = %q", m.argsFor, sess.calls)
	}
}

// handleKeyNotice presses k and returns the notice it leaves.
func (m *runningModel) handleKeyNotice(t *testing.T, k string) string {
	t.Helper()
	m.handleKey(key(k))
	return m.notice
}

func TestLauncherLast(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	m.Update(key("l"))
	if m.notice != "no last session yet" {
		t.Errorf("notice = %q", m.notice)
	}

	m.setLast(&runrstate.LastSession{Names: []string{"backend", "gone"}, Args: map[string]string{"api": "--x"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24}) // tall enough for the wordmark
	if got := strings.Join(launcherScreen(m), "\n"); !strings.Contains(got, "╹┗╸┗━┛╹ ╹╹┗╸    l  last session: backend\n") {
		t.Errorf("no last session under the wordmark:\n%s", got)
	}
	_, cmd := m.Update(key("l"))
	msg := cmd().(startMsg)
	if !slices.Equal(msg.names, []string{"backend"}) || msg.args["api"] != "--x" ||
		msg.notice != "skipped from the last session, not registered any more: gone" {
		t.Errorf("start = %+v", msg)
	}

	// The arguments form starts with the last session's arguments.
	m.Update(key("R"))
	if m.form == nil || m.form.form.fields[1].input.Value() != "--x" {
		t.Error("R not pre-filled from the last session")
	}
	m.Update(key("esc"))

	m.setLast(&runrstate.LastSession{Names: []string{"gone"}})
	if _, cmd := m.Update(key("l")); cmd != nil || m.notice != "nothing from the last session is registered any more" {
		t.Errorf("cmd = %v, notice = %q", cmd, m.notice)
	}
}

func TestAppShowsStartNotice(t *testing.T) {
	cfg := launcherConfig(t)
	app := &appModel{cfg: cfg, launcher: newLauncherModel(cfg, nil, newStyles(terminalTheme)), start: func([]string, map[string]string) (Session, error) {
		return newFakeSession("api"), nil
	}}
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	app.Update(startMsg{names: []string{"api"}, notice: "skipped x"})
	if app.running == nil || app.running.notice != "skipped x" {
		t.Fatal("the running view doesn't show the notice")
	}
}
