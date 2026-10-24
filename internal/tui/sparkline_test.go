package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/supervisor"
)

func TestSparkLevel(t *testing.T) {
	for n, want := range map[int]int{0: 0, 1: 1, 2: 2, 3: 2, 4: 3, 7: 3, 8: 4, 31: 5, 32: 6, 63: 6, 64: 7, 100000: 7} {
		if got := sparkLevel(n); got != want {
			t.Errorf("sparkLevel(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestOutputRateReusesSlots(t *testing.T) {
	var r outputRate
	r.add(1000, 5)
	r.add(1000+rateWindow, 2) // same slot, a window later
	if r.count(1000) != 0 || r.count(1000+rateWindow) != 2 {
		t.Errorf("counts = %d, %d", r.count(1000), r.count(1000+rateWindow))
	}
}

// feed delivers n lines of project at a time.
func feed(m *runningModel, project string, at time.Time, n int) {
	m.now = func() time.Time { return at }
	var events []supervisor.Event
	for range n {
		events = append(events, line(project, "x"))
	}
	m.Update(eventsMsg{events: events})
}

func sparkOf(m *runningModel, row int) string {
	text := screen(m)[row]
	_, after, _ := strings.Cut(text, "s ") // after the uptime
	spark, _, _ := strings.Cut(after, " ")
	return spark
}

func TestSparkline(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	start := testNow()
	m.Update(eventsMsg{events: []supervisor.Event{supervisor.StateEvent{Info: supervisor.ProcessInfo{
		Name: "db", State: supervisor.Running, StartedAt: start.Add(-time.Minute),
	}}}})
	end := start.Add(30 * time.Second)
	feed(m, "db", end.Add(-30*time.Second), 1)  // cell 0: 1 line
	feed(m, "db", end.Add(-20*time.Second), 3)  // cell 2: 3 lines
	feed(m, "db", end.Add(-10*time.Second), 10) // cell 5: 10 lines
	feed(m, "db", end, 100)                     // cell 7: 100 lines
	m.Update(eventsMsg{events: []supervisor.Event{system("db", "not counted")}})
	if got := sparkOf(m, 1); got != "▂▁▃▁▁▅▁█" {
		t.Errorf("sparkline = %q", got)
	}

	// It shifts as time passes, and is flat after a window without output.
	m.now = func() time.Time { return end.Add(4 * time.Second) }
	if got := sparkOf(m, 1); got != "▁▃▁▁▅▁█▁" {
		t.Errorf("4 s later = %q", got)
	}
	m.now = func() time.Time { return end.Add(rateWindow * time.Second) }
	if got := sparkOf(m, 1); got != "▁▁▁▁▁▁▁▁" {
		t.Errorf("a window later = %q", got)
	}
}

func TestSparklineColors(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	at := testNow()
	feed(m, "api", at, 1)
	cells := func() string { return strings.Split(m.render(), "\n")[2] }
	if got := cells(); !strings.Contains(got, "\x1b[2m▁▁▁▁▁▁▁\x1b[m\x1b[36m▂") { // faint, then cyan
		t.Errorf("api sparkline = %q", got)
	}
	m.Update(eventsMsg{events: []supervisor.Event{supervisor.StateEvent{Info: supervisor.ProcessInfo{
		Name: "api", State: supervisor.Crashed, Exit: proc.ExitStatus{Code: 1},
	}}}})
	if got := cells(); !strings.Contains(got, "\x1b[31m▂") {
		t.Errorf("crashed api sparkline = %q", got)
	}
	if got := ansi.Strip(cells()); !strings.Contains(got, "▁▁▁▁▁▁▁▂") {
		t.Errorf("plain = %q", got)
	}
}

func TestSparklineKeepsTicking(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	at := testNow()
	feed(m, "worker", at, 20)
	m.Update(eventsMsg{events: []supervisor.Event{supervisor.StateEvent{Info: supervisor.ProcessInfo{
		Name: "worker", State: supervisor.Crashed, Exit: proc.ExitStatus{Code: 1},
	}}}})
	if m.tickEvery != uptimeEvery {
		t.Errorf("tick %v with recent output, want %v", m.tickEvery, uptimeEvery)
	}
	m.now = func() time.Time { return at.Add(rateWindow * time.Second) }
	m.Update(uiTickMsg{gen: m.tickGen})
	if m.tickEvery != 0 {
		t.Errorf("tick %v once the sparkline is flat", m.tickEvery)
	}
}
