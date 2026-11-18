//go:build !windows

package supervisor

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
)

// stopCmdConfig is a config with one project, api, that ignores SIGTERM
// (so stopping it ends in a kill) and has stopCmd as its stop_cmd.
func stopCmdConfig(t *testing.T, cmd, stopCmd string, timeout time.Duration) *config.Config {
	t.Helper()
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = timeout
	cfg.Projects["api"] = &config.Project{Dir: t.TempDir(), Cmd: cmd, StopCmd: stopCmd, Env: map[string]string{"WHO": "api"}}
	return cfg
}

const stubborn = "trap '' TERM; echo ready; while :; do sleep 0.05; done"

func TestStopCmdAfterTimeoutKill(t *testing.T) {
	cfg := stopCmdConfig(t, stubborn, "echo cleaning up $WHO in $(basename $PWD); echo oops >&2", testTimeout)
	s, r := newTestSupervisorWithConfig(t, cfg)
	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "api", "out:ready")
	if err := s.Stop("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "api", Stopped)

	logs := logLines(s, "api")
	want := []string{
		"fail:still running after 300ms, killing",
		"sys:running stop_cmd: echo cleaning up $WHO in $(basename $PWD); echo oops >&2",
		"out:cleaning up api in " + filepath.Base(cfg.Projects["api"].Dir), // its dir and env
		"sys:stop_cmd done (exit code 0)",
		"sys:stopped (signal: killed)",
	}
	at := 0
	for _, l := range logs {
		if at < len(want) && l == want[at] {
			at++
		}
	}
	if at < len(want) || !containsLine(logs, "err:oops") { // stderr can come before stdout
		t.Errorf("logs = %q\nwant in order %q, and err:oops", logs, want)
	}
	// Stopping until the stop_cmd is done, then Stopped once.
	if states := r.states("api"); slices.Index(states, Stopping) < 0 || states[len(states)-1] != Stopped || slices.Index(states, Stopped) != len(states)-1 {
		t.Errorf("states = %v", states)
	}
}

func TestStopCmdNotAfterGracefulStop(t *testing.T) {
	cfg := stopCmdConfig(t, "echo ready; exec sleep 30", "echo cleaning up", longTimeout)
	s, r := newTestSupervisorWithConfig(t, cfg)
	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "api", "out:ready")
	s.Stop("api")
	r.waitState(t, "api", Stopped)
	// Exits and crashes don't run it either.
	cfg.Projects["api"].Cmd = "echo ready; exit 3"
	s.Start("api")
	r.waitState(t, "api", Crashed)
	if logs := logLines(s, "api"); containsLine(logs, "sys:running stop_cmd: echo cleaning up") {
		t.Errorf("stop_cmd ran: %q", logs)
	}
}

func TestStopCmdAfterKill(t *testing.T) {
	cfg := stopCmdConfig(t, stubborn, "echo cleaning up; exit 4", longTimeout)
	s, r := newTestSupervisorWithConfig(t, cfg)
	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "api", "out:ready")
	s.Kill("api")
	info := r.waitState(t, "api", Stopped)
	logs := logLines(s, "api")
	if !containsLine(logs, "out:cleaning up") || !containsLine(logs, "fail:stop_cmd failed (exit code 4)") {
		t.Errorf("logs = %q", logs)
	}
	if info.Errors != 1 || info.Exit.Signal != "killed" {
		t.Errorf("info = %+v", info)
	}
}

func TestStopCmdTimeout(t *testing.T) {
	old := stopCmdTimeout
	stopCmdTimeout = 100 * time.Millisecond
	t.Cleanup(func() { stopCmdTimeout = old })
	cfg := stopCmdConfig(t, stubborn, "exec sleep 30", longTimeout)
	s, r := newTestSupervisorWithConfig(t, cfg)
	s.Start("api")
	waitLog(t, s, "api", "out:ready")
	start := time.Now()
	s.Kill("api")
	r.waitState(t, "api", Stopped)
	if d := time.Since(start); d > quickLimit {
		t.Errorf("took %v", d)
	}
	if logs := logLines(s, "api"); !containsLine(logs, "fail:stop_cmd still running after 100ms, killing it") || !containsLine(logs, "fail:stop_cmd failed (signal: killed)") {
		t.Errorf("logs = %q", logs)
	}
}

func TestStopCmdFailsToStart(t *testing.T) {
	cfg := stopCmdConfig(t, stubborn, "echo never", longTimeout)
	s, r := newTestSupervisorWithConfig(t, cfg)
	s.Start("api")
	waitLog(t, s, "api", "out:ready")
	os.RemoveAll(cfg.Projects["api"].Dir)
	s.Kill("api")
	r.waitState(t, "api", Stopped)
	if logs := logLines(s, "api"); !containsLine(logs, "fail:stop_cmd failed to start: dir "+cfg.Projects["api"].Dir+" does not exist") {
		t.Errorf("logs = %q", logs)
	}
}

func TestStopAllWaitsForStopCmd(t *testing.T) {
	cfg := stopCmdConfig(t, stubborn, "sleep 0.3; echo cleaned > done", testTimeout)
	marker := filepath.Join(cfg.Projects["api"].Dir, "done")
	s, _ := newTestSupervisorWithConfig(t, cfg)
	s.Start("api")
	waitLog(t, s, "api", "out:ready")
	select {
	case <-s.StopAll():
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll did not finish")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("StopAll finished before the stop_cmd: %v", err)
	}
}

func TestCloseWaitsForStopCmd(t *testing.T) {
	cfg := stopCmdConfig(t, "echo ready; exec sleep 30", "sleep 0.3; echo cleaned > done", longTimeout)
	marker := filepath.Join(cfg.Projects["api"].Dir, "done")
	s, _ := newTestSupervisorWithConfig(t, cfg)
	s.Start("api")
	waitLog(t, s, "api", "out:ready")
	s.Close() // kills, so the stop_cmd runs
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("Close returned before the stop_cmd: %v", err)
	}
}

func TestKillAllKillsStopCmd(t *testing.T) {
	cfg := stopCmdConfig(t, stubborn, "echo cleaning; exec sleep 30", longTimeout)
	s, r := newTestSupervisorWithConfig(t, cfg)
	s.Start("api")
	waitLog(t, s, "api", "out:ready")
	s.Kill("api")
	waitLog(t, s, "api", "out:cleaning")
	start := time.Now()
	s.KillAll() // forcing again: the stop_cmd too
	r.waitState(t, "api", Stopped)
	if d := time.Since(start); d > quickLimit {
		t.Errorf("took %v", d)
	}
}

func TestTrackerGetsStopCmd(t *testing.T) {
	cfg := stopCmdConfig(t, "exec sleep 30", "docker compose down", longTimeout)
	tr := &fakeTracker{}
	s := New(cfg, Options{BaseEnv: []string{"PATH=" + os.Getenv("PATH")}, Tracker: tr})
	r := record(s)
	t.Cleanup(s.Close)
	s.Start("api")
	r.waitState(t, "api", Running)
	want := "spawned api exec sleep 30 true stop_cmd docker compose down in " + filepath.Base(cfg.Projects["api"].Dir)
	if got := tr.get(); len(got) == 0 || got[0] != want {
		t.Errorf("calls = %q, want %q first", got, want)
	}
}
