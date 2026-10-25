package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeLeftovers records what was done with the leftovers.
type fakeLeftovers struct{ calls []string }

func (f *fakeLeftovers) get() *Leftovers {
	return &Leftovers{
		What:   "2 processes left over from a previous runr session",
		Which:  "api (pid 4123), db (pid 4120)",
		Kill:   func() string { f.calls = append(f.calls, "kill"); return "stopped 2 leftover processes" },
		Forget: func() { f.calls = append(f.calls, "forget") },
	}
}

// answer presses k and runs the command it returns, feeding its message
// back to the model.
func answer(m tea.Model, k string) {
	_, cmd := m.Update(key(k))
	if cmd != nil {
		m.Update(cmd())
	}
}

func TestRunningLeftoversKill(t *testing.T) {
	m, sess := newTestModel(t, 120, 12)
	f := &fakeLeftovers{}
	m.askLeftovers(f.get())
	if got := helpRow(m); !strings.Contains(got, "Kill 2 processes left over from a previous runr session? api (pid 4123), db (pid 4120)") ||
		!strings.Contains(got, "enter  kill") || !strings.Contains(got, "esc  leave them") {
		t.Errorf("help = %q", got)
	}
	// Other keys don't get past the question; nothing starts before it.
	m.Update(key("s"))
	if len(sess.calls) != 0 || m.confirm == nil {
		t.Fatalf("calls %q before the answer", sess.calls)
	}

	_, cmd := m.Update(key("y"))
	if m.notice != "stopping the leftover processes…" || m.confirm != nil {
		t.Errorf("notice %q", m.notice)
	}
	m.Update(cmd())
	if !slices.Equal(f.calls, []string{"kill"}) || !slices.Equal(sess.calls, []string{"start backend"}) {
		t.Errorf("leftovers %q, session %q", f.calls, sess.calls)
	}
	if m.notice != "stopped 2 leftover processes" {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestRunningLeftoversLeave(t *testing.T) {
	m, sess := newTestModel(t, 120, 12)
	f := &fakeLeftovers{}
	m.askLeftovers(f.get())
	answer(m, "n")
	if !slices.Equal(f.calls, []string{"forget"}) || !slices.Equal(sess.calls, []string{"start backend"}) || m.notice != "" {
		t.Errorf("leftovers %q, session %q, notice %q", f.calls, sess.calls, m.notice)
	}
}

func TestLauncherLeftovers(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	f := &fakeLeftovers{}
	m.askLeftovers(f.get())
	if got := strings.Join(launcherScreen(m), "\n"); !strings.Contains(got, " Kill 2 processes left over from a previous runr session? api") {
		t.Errorf("screen:\n%s", got)
	}
	_, cmd := m.Update(key("enter"))
	if !m.cleaning || m.ask != nil {
		t.Fatal("kill not under way")
	}
	// Starting waits for the kill.
	if _, start := m.Update(key("enter")); start != nil || m.notice != "the leftover processes are still being stopped" {
		t.Errorf("started while cleaning; notice %q", m.notice)
	}
	m.Update(cmd())
	if m.cleaning || m.notice != "stopped 2 leftover processes" || !slices.Equal(f.calls, []string{"kill"}) {
		t.Errorf("cleaning %v, notice %q, calls %q", m.cleaning, m.notice, f.calls)
	}

	m = newTestLauncher(t, launcherConfig(t))
	f = &fakeLeftovers{}
	m.askLeftovers(f.get())
	answer(m, "esc")
	if m.cleaning || m.ask != nil || !slices.Equal(f.calls, []string{"forget"}) {
		t.Errorf("leave: cleaning %v, calls %q", m.cleaning, f.calls)
	}
}
