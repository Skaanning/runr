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

func TestStatusTextOneShot(t *testing.T) {
	now := time.Now()
	exited := supervisor.ProcessInfo{State: supervisor.Exited, StartedAt: now.Add(-time.Minute)}
	if got, failed := statusText(exited, true, now); got != "done" || failed {
		t.Errorf("finished one-shot: %q, failed %v", got, failed)
	}
	if got, _ := statusText(exited, false, now); got != "" {
		t.Errorf("exited process that isn't a one-shot: %q", got)
	}
	starting := supervisor.ProcessInfo{State: supervisor.Starting, StartedAt: now.Add(-3 * time.Second)}
	if got, _ := statusText(starting, true, now); got != "3s" {
		t.Errorf("running one-shot: %q, want its uptime", got)
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
	m, _ := newTestModel(t, 100, 16)
	aliveEvents(m)
	want := []string{
		"│ ▌● db        4m02s ▁▁▁▁▁▁▁▁ ││",
		"│  ⠋ api         45s ▁▁▁▁▁▁▁▁ ││",
		"│  ✗ Worker exit 127 ▁▁▁▁▁▁▁▁ ││",
		"│  ⊘ web     blocked ▁▁▁▁▁▁▁▁ ││",
		"│  ✗ cron    crashed ▁▁▁▁▁▁▁▁ ││",
		"│  ■ docs            ▁▁▁▁▁▁▁▁ ││",
	}
	for i, w := range want {
		if got := screen(m)[1+2*i]; !strings.HasPrefix(got, w) { // two lines per process
			t.Errorf("row %d = %q, want prefix %q", i, got, w)
		}
	}
	// Uptimes count on.
	m.now = func() time.Time { return testNow().Add(time.Minute) }
	if got := screen(m)[1]; !strings.HasPrefix(got, "│ ▌● db        5m02s ▁▁▁▁▁▁▁▁ ││") {
		t.Errorf("a minute later: %q", got)
	}
}

func TestSidebarColumnsWhenNarrow(t *testing.T) {
	for _, tt := range []struct {
		width, row int
		want       string // the db row
	}{
		{120, 1, "│ ▌● db      4m02s ▁▁▁▁▁▁▁▁ ││"},
		{120, 2, "│ ▌  main                   ││"}, // git on the second line
		{56, 1, "│ ▌● db        4m02s ││"},         // the sparkline goes first
		{56, 2, "│ ▌  main            ││"},
		{48, 2, " ▌ ● db      4m02s │"}, // no panes: one line, no room for the git column alone
		{40, 2, " ▌ ● db     │"},        // nor for the uptime
	} {
		m, _ := newTestModel(t, tt.width, 12)
		m.git = newGitTracker(testGit())
		loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
		m.Update(eventsMsg{events: []supervisor.Event{supervisor.StateEvent{Info: supervisor.ProcessInfo{
			Name: "db", State: supervisor.Running, StartedAt: testNow().Add(-4*time.Minute - 2*time.Second),
		}}}})
		if got := screen(m)[tt.row]; !strings.HasPrefix(got, tt.want) {
			t.Errorf("width %d: %q, want prefix %q", tt.width, got, tt.want)
		}
	}
}

func TestTwoLineLayout(t *testing.T) {
	// prefix 4, names 4+6+1 = 11, status 6, git label 12 wide.
	for _, tt := range []struct {
		limit int
		want  sidebarCols
	}{
		{40, sidebarCols{width: 27, status: 6, spark: 8, twoLine: true}},
		{26, sidebarCols{width: 18, status: 6, twoLine: true}}, // the sparkline goes first
		{17, sidebarCols{width: 12, twoLine: true}},            // then the status column (11, at least 12)
		{9, sidebarCols{width: 12, twoLine: true}},             // names truncated, never below 12
	} {
		if got := twoLineLayout(4, 11, tt.limit, 0, 6); got != tt.want {
			t.Errorf("limit %d: %+v, want %+v", tt.limit, got, tt.want)
		}
	}
	// The sidebar grows to fit a git label on the second line, up to maxGitWidth.
	if got := twoLineLayout(4, 11, 40, 30, 6); got.width != 4+maxGitWidth+1 {
		t.Errorf("wide git label: width %d, want %d", got.width, 4+maxGitWidth+1)
	}
	if got := twoLineLayout(4, 11, 40, 20, 0); got.width != 4+20+1 || got.spark != 8 {
		t.Errorf("git label wider than line 1: %+v", got)
	}
}

func TestSpinnersMoveInStep(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	aliveEvents(m)
	for i, frame := range []string{"⠋", "⠙", "⠹"} {
		m.now = func() time.Time { return testNow().Add(time.Duration(i) * animateEvery) }
		rows := screen(m)
		if !strings.HasPrefix(rows[3], "│  "+frame+" api") || !strings.Contains(rows[len(rows)-2], frame+" 1") {
			t.Errorf("frame %d: sidebar %q, status bar %q", i, rows[3], rows[len(rows)-2])
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
