package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/supervisor"
)

func crash(name string, code int) supervisor.Event {
	return supervisor.StateEvent{Info: supervisor.ProcessInfo{Name: name, State: supervisor.Crashed, Exit: proc.ExitStatus{Code: code}}}
}

// toastText returns the rows of the log column that belong to toasts.
func toastText(m *runningModel) []string {
	var out []string
	for _, r := range logRows(m) {
		if i := strings.IndexAny(r, "╭│╰"); i >= 0 {
			out = append(out, strings.TrimRight(r[i:], " "))
		}
	}
	return out
}

func TestToastShownOnCrash(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{
		state("db", supervisor.Running),
		line("db", strings.Repeat("x", 80)),
		crash("worker", 1),
	}})
	want := []string{
		"╭────────────────────────────────╮",
		"│ ✗ Worker crashed · exit code 1 │",
		"│   ! select it                  │",
		"╰────────────────────────────────╯",
	}
	if got := toastText(m); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("toast:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	rows := screen(m)
	if got := rows[12]; !strings.HasSuffix(got, "╰────────────────────────────────╯ │") {
		t.Errorf("toast isn't at the bottom right of the log pane: %q", got)
	}
	if got := rows[1]; !strings.Contains(got, strings.Repeat("x", 20)) {
		t.Errorf("log rows above the toast are covered: %q", got)
	}
	for i, row := range strings.Split(m.render(), "\n") {
		if w := ansi.StringWidth(row); w != 96 && i < 15 {
			t.Errorf("row %d is %d cells wide", i, w)
		}
	}
}

func TestToastHint(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{crash("db", 2)}})
	if got := toastText(m)[2]; got != "│   r restart  tab see its log   │" {
		t.Errorf("hint for the selected project = %q", got)
	}
	m.showSingle("db")
	if got := toastText(m)[2]; got != "│   r restart                    │" {
		t.Errorf("hint in its own log = %q", got)
	}
}

func TestToastBlocked(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.cfg.Projects["api"].DependsOn = []string{"db"}
	m.Update(eventsMsg{events: []supervisor.Event{crash("db", 1), state("api", supervisor.Blocked)}})
	got := toastText(m)
	if len(got) != 8 || got[5] != "│ ⊘ api blocked · db crashed     │" {
		t.Errorf("toasts:\n%s", strings.Join(got, "\n"))
	}
	if got[1] != "│ ✗ db crashed · exit code 1     │" {
		t.Errorf("crash toast = %q", got[1])
	}

	m.cfg.Projects["api"].DependsOn = nil
	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Waiting), state("api", supervisor.Blocked)}})
	if got := toastText(m); got[5] != "│ ⊘ api blocked · a dependency failed │" {
		t.Errorf("toast without a known dependency = %q", got[5])
	}
}

func TestToastExpires(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	now := testNow()
	m.now = func() time.Time { return now }
	_, cmd := m.Update(eventsMsg{events: []supervisor.Event{crash("worker", 1)}, closed: true})
	if cmd == nil || len(m.toasts) != 1 {
		t.Fatalf("no toast or expiry command: %d toasts", len(m.toasts))
	}
	now = now.Add(toastFor - time.Millisecond)
	m.Update(toastMsg{})
	if len(toastText(m)) != 4 {
		t.Fatal("toast expired early")
	}
	now = now.Add(time.Millisecond)
	m.Update(toastMsg{})
	if got := toastText(m); len(got) != 0 {
		t.Fatalf("toast still shown after %v:\n%s", toastFor, strings.Join(got, "\n"))
	}
}

func TestToastDismissedByKey(t *testing.T) {
	m, sess := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{crash("db", 1)}})
	m.Update(key("r"))
	if len(toastText(m)) != 0 {
		t.Error("toast still shown after a key")
	}
	if strings.Join(sess.calls, ",") != "restart db" {
		t.Errorf("the key wasn't handled as usual: %v", sess.calls)
	}
}

func TestToastsStack(t *testing.T) {
	m, _ := newTestModel(t, 96, 18)
	m.Update(eventsMsg{events: []supervisor.Event{crash("db", 1), crash("api", 2), crash("worker", 3)}})
	m.Update(eventsMsg{events: []supervisor.Event{crash("web", 4)}})
	var titles []string
	for i, r := range toastText(m) {
		if i%4 == 1 {
			titles = append(titles, strings.TrimSpace(strings.Trim(r, "│")))
		}
	}
	want := "✗ api crashed · exit code 2|✗ Worker crashed · exit code 3|✗ web crashed · exit code 4"
	if got := strings.Join(titles, "|"); got != want {
		t.Errorf("stacked toasts = %q, want %q", got, want)
	}

	// The same project crashing again replaces its toast and moves it to the bottom.
	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Running), crash("api", 5)}})
	if got := m.toasts; len(got) != 3 || got[2].name != "api" || got[0].name != "worker" {
		t.Errorf("toasts after api crashed again: %+v", got)
	}

	// Only as many as fit are shown, the newest.
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 12}) // 8 log rows
	if got := toastText(m); len(got) != 8 || !strings.Contains(got[5], "api crashed") {
		t.Errorf("toasts in 8 rows:\n%s", strings.Join(got, "\n"))
	}
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 11}) // 7 log rows
	if got := toastText(m); len(got) != 0 {
		t.Errorf("toasts in 7 rows:\n%s", strings.Join(got, "\n"))
	}
}

func TestToastSelectKey(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{crash("api", 1), crash("worker", 1)}})
	m.Update(key("!"))
	if e := m.selectedEntry(); e.info.Name != "worker" {
		t.Errorf("selected %s, want worker (the newest toast)", e.info.Name)
	}
	if len(m.toasts) != 0 {
		t.Error("toasts still shown after !")
	}
	m.Update(key("!")) // no toasts: nothing happens
	if e := m.selectedEntry(); e.info.Name != "worker" {
		t.Errorf("! without toasts selected %s", e.info.Name)
	}

	// In the single view, ! switches to the project's log.
	m.Update(key("tab"))
	m.Update(eventsMsg{events: []supervisor.Event{crash("db", 1)}})
	m.Update(key("!"))
	if m.singleFor != "db" {
		t.Errorf("single view shows %q after !, want db", m.singleFor)
	}
}

func TestNoToastsDuringShutdown(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{crash("api", 1)}})
	m.Update(key("q"))
	m.Update(eventsMsg{events: []supervisor.Event{crash("worker", 1)}})
	if len(m.toasts) != 0 {
		t.Errorf("toasts during shutdown: %+v", m.toasts)
	}
}

func TestNoToastForUnchangedState(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{crash("api", 1)}})
	m.toasts = nil
	m.Update(eventsMsg{events: []supervisor.Event{crash("api", 1)}}) // e.g. a restart count changed
	if len(m.toasts) != 0 {
		t.Errorf("toast for a state that didn't change: %+v", m.toasts)
	}
}

func TestToastHiddenUnderPicker(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(key("+"))
	m.Update(eventsMsg{events: []supervisor.Event{crash("api", 1)}})
	for _, r := range screen(m) {
		if strings.Contains(r, "crashed ·") {
			t.Fatalf("toast drawn over the picker: %q", r)
		}
	}
}

func TestToastBackground(t *testing.T) {
	m, _ := newTestModel(t, 96, 16)
	m.Update(eventsMsg{events: []supervisor.Event{crash("api", 1)}})
	if !strings.Contains(m.render(), m.st.alert) || m.st.alert == "" {
		t.Error("toast has no background tint on 256 colors")
	}
}
