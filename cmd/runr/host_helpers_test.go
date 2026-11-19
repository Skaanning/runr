package main

import (
	"os"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/control"
	"github.com/Skaanning/runr/internal/state"
	"github.com/Skaanning/runr/internal/supervisor"
)

// hostEnv registers projects (each in a temp dir) for hosts, which are the
// test binary acting as runr.
func hostEnv(t *testing.T, projects map[string]*config.Project) {
	t.Helper()
	testEnv(t)
	dir, err := os.MkdirTemp("", "rs") // short enough for sockets
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv(state.EnvVar, dir)
	t.Setenv(mainEnv, "1")
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = 5 * time.Second
	for name, p := range projects {
		p.Dir = t.TempDir()
		cfg.Projects[name] = p
	}
	path, _ := config.Path()
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
}

// startTestHost starts a host and stops it when the test ends.
func startTestHost(t *testing.T, names []string, o hostOptions) *host {
	t.Helper()
	h, err := startHost(names, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-h.exited:
		default:
			h.process.Kill() // its projects go with it (Unix: they're stopped by TERM first)
			waitExited(t, h)
		}
	})
	return h
}

func waitExited(t *testing.T, h *host) {
	t.Helper()
	select {
	case <-h.exited:
	case <-time.After(10 * time.Second):
		t.Fatalf("host %d still running", h.pid)
	}
}

func stillRunning(t *testing.T, h *host) {
	t.Helper()
	select {
	case <-h.exited:
		t.Fatalf("host ended: %v", h.err)
	case <-time.After(100 * time.Millisecond):
	}
}

// hostStatus asks a host for its status.
func hostStatus(t *testing.T, h *host) control.Status {
	t.Helper()
	var st control.Status
	err := control.Do(h.path, control.Request{Op: control.OpStatus}, func(r control.Reply) error {
		st = *r.Status
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// waitState waits until the host's project is in state.
func waitState(t *testing.T, h *host, name string, state supervisor.State) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if p, ok := findProcess(hostStatus(t, h), name); ok && p.State == state.String() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never %v: %+v", name, state, hostStatus(t, h).Processes)
		}
	}
}
