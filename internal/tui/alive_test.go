package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/supervisor"
)

func TestUptime(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                             "0s",
		45 * time.Second:              "45s",
		4*time.Minute + 2*time.Second: "4m02s",
		time.Hour + 12*time.Minute + 59*time.Second:  "1h12m",
		3*24*time.Hour + 4*time.Hour + 5*time.Minute: "3d04h",
		-time.Second: "0s", // clock skew
	} {
		if got := uptime(d); got != want {
			t.Errorf("uptime(%v) = %q, want %q", d, got, want)
		}
	}
}

// aliveEvents puts the test model's processes and two more in various states.
func aliveEvents(m *runningModel) {
	now := m.now()
	info := func(name string, s supervisor.State, started time.Duration, exit proc.ExitStatus) supervisor.Event {
		i := supervisor.ProcessInfo{Name: name, State: s, Exit: exit}
		if started > 0 {
			i.StartedAt = now.Add(-started)
		}
		return supervisor.StateEvent{Info: i}
	}
	m.Update(eventsMsg{events: []supervisor.Event{
		info("db", supervisor.Running, 4*time.Minute+2*time.Second, proc.ExitStatus{}),
		info("api", supervisor.Starting, 45*time.Second, proc.ExitStatus{}),
		info("worker", supervisor.Crashed, time.Minute, proc.ExitStatus{Code: 127}),
		info("web", supervisor.Blocked, 0, proc.ExitStatus{}),
		info("cron", supervisor.Crashed, time.Minute, proc.ExitStatus{Code: -1, Signal: "killed"}),
		info("docs", supervisor.Stopped, time.Minute, proc.ExitStatus{}),
	}})
}

func TestSidebarStatusColumn(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	aliveEvents(m)
	want := []string{
		"│ ▌● db        4m02s ││",
		"│  ⠋ api         45s ││",
		"│  ✗ Worker exit 127 ││",
		"│  ⊘ web     blocked ││",
		"│  ✗ cron    crashed ││",
		"│  ■ docs            ││",
	}
	for i, w := range want {
		if got := screen(m)[1+i]; !strings.HasPrefix(got, w) {
			t.Errorf("row %d = %q, want prefix %q", i, got, w)
		}
	}
	// Uptimes count on.
	m.now = func() time.Time { return testNow().Add(time.Minute) }
	if got := screen(m)[1]; !strings.HasPrefix(got, "│ ▌● db        5m02s ││") {
		t.Errorf("a minute later: %q", got)
	}
}

func TestStatusColumnDroppedBeforeGit(t *testing.T) {
	for _, tt := range []struct {
		width int
		row   string // the db row
	}{
		{120, "│ ▌● db      4m02s main            ││"},
		{80, "│ ▌● db     main           ││"}, // no room for both: the uptime goes
		{56, "│ ▌● db      4m02s ││"},         // no room for the git column alone
		{50, "│ ▌● db      ││"},               // nor for the uptime
	} {
		m, _ := newTestModel(t, tt.width, 12)
		m.git = newGitTracker(testGit())
		loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
		m.Update(eventsMsg{events: []supervisor.Event{supervisor.StateEvent{Info: supervisor.ProcessInfo{
			Name: "db", State: supervisor.Running, StartedAt: testNow().Add(-4*time.Minute - 2*time.Second),
		}}}})
		if got := screen(m)[1]; !strings.HasPrefix(got, tt.row) {
			t.Errorf("width %d: %q, want prefix %q", tt.width, got, tt.row)
		}
	}
}

func TestSpinnersMoveInStep(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	aliveEvents(m)
	for i, frame := range []string{"⠋", "⠙", "⠹"} {
		m.now = func() time.Time { return testNow().Add(time.Duration(i) * animateEvery) }
		rows := screen(m)
		if !strings.HasPrefix(rows[2], "│  "+frame+" api") || !strings.Contains(rows[len(rows)-2], frame+" 1") {
			t.Errorf("frame %d: sidebar %q, status bar %q", i, rows[2], rows[len(rows)-2])
		}
	}
}

func TestTickSpeed(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	if m.tickEvery != 0 {
		t.Fatalf("tick %v before anything runs", m.tickEvery)
	}
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running), state("api", supervisor.Starting)}})
	if m.tickEvery != animateEvery {
		t.Errorf("tick %v while starting, want %v", m.tickEvery, animateEvery)
	}
	gen := m.tickGen
	m.Update(eventsMsg{events: []supervisor.Event{state("api", supervisor.Running), state("worker", supervisor.Running)}})
	if m.tickEvery != uptimeEvery {
		t.Errorf("tick %v once everything runs, want %v", m.tickEvery, uptimeEvery)
	}
	if _, cmd := m.Update(uiTickMsg{gen: gen}); cmd != nil {
		t.Error("an old tick scheduled another one")
	}
	if _, cmd := m.Update(uiTickMsg{gen: m.tickGen}); cmd == nil {
		t.Error("the current tick did not schedule the next one")
	}
	m.Update(eventsMsg{events: []supervisor.Event{
		state("db", supervisor.Stopped), state("api", supervisor.Exited), state("worker", supervisor.Crashed),
	}})
	if m.tickEvery != 0 {
		t.Errorf("tick %v after everything stopped", m.tickEvery)
	}
	if _, cmd := m.Update(uiTickMsg{gen: m.tickGen}); cmd != nil {
		t.Error("ticking with nothing to animate")
	}
}
