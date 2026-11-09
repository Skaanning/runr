package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/supervisor"
)

// attachModel returns a running view where api runs on a terminal and is
// selected.
func attachModel(t *testing.T) (*runningModel, *fakeSession) {
	t.Helper()
	m, sess := newTestModel(t, 80, 12)
	m.cfg.Projects["api"].PTY = true
	m.Update(eventsMsg{events: []supervisor.Event{
		state("db", supervisor.Running),
		state("api", supervisor.Running),
		line("api", "starting deploy"),
	}})
	m.Update(key("down"))
	return m, sess
}

func runningText(m *runningModel) string { return strings.Join(screen(m), "\n") }

func TestAttachTypesIntoTheProgram(t *testing.T) {
	m, sess := attachModel(t)
	if !strings.Contains(runningText(m), "i  attach") {
		t.Errorf("no attach hint:\n%s", runningText(m))
	}
	m.Update(eventsMsg{events: []supervisor.Event{supervisor.PendingEvent{Project: "api", Text: "branch: "}}})
	if !strings.Contains(runningText(m), "api    branch:") {
		t.Fatalf("the prompt isn't shown in the combined view:\n%s", runningText(m))
	}

	m.Update(key("i"))
	if m.attached != "api" || m.single == nil || m.singleFor != "api" {
		t.Fatalf("attached %q, single view of %q", m.attached, m.singleFor)
	}
	scr := runningText(m)
	if !strings.Contains(scr, "ATTACHED") || !strings.Contains(scr, "ctrl+q  detach") || !strings.Contains(scr, "branch:") {
		t.Errorf("attached screen:\n%s", scr)
	}

	// Every key goes to the program, runr's own too.
	for _, k := range []string{"f", "x", "backspace", "q", "enter", "ctrl+c", "tab", "up"} {
		m.Update(key(k))
	}
	m.Update(tea.PasteMsg{Content: "a\nb\r\nc"})
	if got, want := sess.typed["api"], "fx\x7fq\r\x03\t\x1b[Aa\rb\rc"; got != want {
		t.Errorf("typed %q, want %q", got, want)
	}
	if sess.stopAll != 0 || m.stopping {
		t.Error("ctrl+c while attached stopped the session")
	}

	// The echo shows in the unfinished line; then the line is complete.
	m.Update(eventsMsg{events: []supervisor.Event{supervisor.PendingEvent{Project: "api", Text: "branch: feature/x"}}})
	if !strings.Contains(runningText(m), "branch: feature/x") {
		t.Errorf("echo not shown:\n%s", runningText(m))
	}
	m.Update(eventsMsg{events: []supervisor.Event{
		supervisor.PendingEvent{Project: "api"},
		supervisor.LineEvent{Line: supervisor.LogLine{Project: "api", Text: "branch: feature/x", Seq: 7}},
	}})
	if n := strings.Count(runningText(m), "branch: feature/x"); n != 1 {
		t.Errorf("the completed line is shown %d times:\n%s", n, runningText(m))
	}

	m.Update(key("ctrl+q"))
	if m.attached != "" || m.single != nil {
		t.Errorf("after ctrl+q: attached %q, single %v", m.attached, m.single != nil)
	}
	m.Update(key("q")) // runr's keys again
	if sess.stopAll != 1 {
		t.Error("q after detaching didn't stop")
	}
}

func TestAttachKeepsTheSingleView(t *testing.T) {
	m, _ := attachModel(t)
	m.Update(key("tab")) // api's own view
	m.Update(key("i"))
	m.Update(key("ctrl+q"))
	if m.single == nil || m.singleFor != "api" {
		t.Error("detaching left the view attaching started in")
	}
}

func TestAttachRefused(t *testing.T) {
	m, sess := attachModel(t)
	m.Update(key("up")) // db: no terminal
	m.Update(key("i"))
	if m.attached != "" || !strings.Contains(runningText(m), "db doesn't take input: set pty = true") {
		t.Errorf("db:\n%s", runningText(m))
	}
	if strings.Contains(runningText(m), "i  attach") {
		t.Error("attach hint for a project without a terminal")
	}

	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Exited)}})
	m.Update(key("down"))
	m.Update(key("i"))
	if m.attached != "" || !strings.Contains(runningText(m), "api isn't running") {
		t.Errorf("exited api:\n%s", runningText(m))
	}
	if len(sess.typed) != 0 {
		t.Errorf("typed %q", sess.typed)
	}
}

func TestAttachEnds(t *testing.T) {
	m, sess := attachModel(t)
	m.Update(key("i"))
	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Crashed)}})
	if m.attached != "" || !strings.Contains(runningText(m), "api isn't running any more") {
		t.Errorf("after the program ended:\n%s", runningText(m))
	}

	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Running)}})
	m.Update(key("i"))
	sess.inputErr = errors.New("api: the program isn't reading its input")
	m.Update(key("x"))
	if m.attached != "" || !strings.Contains(runningText(m), "isn't reading its input") {
		t.Errorf("after an input error:\n%s", runningText(m))
	}

	sess.inputErr = nil
	m.Update(key("i"))
	m.interrupt() // a signal: everything stops
	if m.attached != "" {
		t.Error("still attached while stopping")
	}
	if sess.stopAll != 1 {
		t.Errorf("stopAll = %d", sess.stopAll)
	}
}
