//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/control"
	"github.com/Skaanning/runr/internal/sessions"
	"github.com/Skaanning/runr/internal/supervisor"
)

// hostEnv registers projects (each in a temp dir) for hosts, which are the
// test binary acting as runr.
func hostEnv(t *testing.T, projects map[string]*config.Project) {
	t.Helper()
	controlEnv(t)
	t.Setenv(mainEnv, "1")
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = 2 * time.Second
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
			syscall.Kill(h.pid, syscall.SIGTERM)
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

func TestUpDetached(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "echo api up; exec sleep 30"}})
	out, _, err := run(t, "up", "-d", "api")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(out, "started session "), " · runr attach to watch it\n"))
	if err != nil {
		t.Fatalf("output %q", out)
	}
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })

	status := mustRun(t, "status")
	if !strings.Contains(status, "session "+strconv.Itoa(pid)+" · api ·") || !strings.Contains(status, "attached: 0") || !strings.Contains(status, "running") {
		t.Errorf("status:\n%s", status)
	}
	if out := mustRun(t, "down"); out != "session "+strconv.Itoa(pid)+" stopped\n" {
		t.Errorf("down: %q", out)
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the host is still running after runr down")
		}
	}
	// A clean end leaves no log behind.
	dir, _ := hostsDir()
	if logs, _ := filepath.Glob(filepath.Join(dir, "*.log")); len(logs) > 0 {
		t.Errorf("host logs left: %v", logs)
	}
}

func TestUpDetachedFlags(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "exec sleep 30"}})
	for _, args := range [][]string{{"up", "-d", "--plain", "api"}, {"up", "-d", "--in-process", "api"}} {
		if _, _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "leave out") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestHostFailsToStart(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "exec sleep 30"}})
	_, err := startHost([]string{"nope"}, hostOptions{})
	if err == nil || !strings.Contains(err.Error(), "nope") || strings.HasPrefix(err.Error(), "runr:") {
		t.Fatalf("err = %v", err)
	}
	dir, _ := hostsDir()
	if logs, _ := filepath.Glob(filepath.Join(dir, "*.log")); len(logs) > 0 {
		t.Errorf("host logs left: %v", logs)
	}
}

func TestHostArgs(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "echo args:"}})
	h := startTestHost(t, []string{"api"}, hostOptions{args: map[string]string{"api": "-x 'a b'"}})
	waitState(t, h, "api", supervisor.Exited)
	if p, _ := findProcess(hostStatus(t, h), "api"); p.Args != "-x 'a b'" {
		t.Errorf("args = %q", p.Args)
	}
	out := mustRun(t, "logs", "api")
	if !strings.Contains(out, "args: -x a b") {
		t.Errorf("logs:\n%s", out)
	}
	stillRunning(t, h) // a host stays up when its projects are done
}

func TestHostViewRule(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "exec sleep 30"}})
	h := startTestHost(t, []string{"api"}, hostOptions{forView: true})
	waitState(t, h, "api", supervisor.Running)

	// d, then attach again: the session goes on.
	r, err := control.Attach(h.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Detach(); err != nil {
		t.Fatal(err)
	}
	stillRunning(t, h)
	if st := hostStatus(t, h); st.Attached != 0 || !st.Host {
		t.Errorf("status after detaching: %+v", st)
	}
	// Closing the last view without d (its terminal was closed) stops it.
	r, err = control.Attach(h.path)
	if err != nil {
		t.Fatal(err)
	}
	r.Drop(errors.New("hung up"))
	waitExited(t, h)
}

func TestHostQuitFromView(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "exec sleep 30"}})
	h := startTestHost(t, []string{"api"}, hostOptions{forView: true})
	r, err := control.Attach(h.path)
	if err != nil {
		t.Fatal(err)
	}
	select { // q in the view
	case <-r.StopAll():
	case <-time.After(10 * time.Second):
		t.Fatal("the session didn't end")
	}
	if p := r.Processes(); len(p) != 1 || p[0].State != supervisor.Stopped {
		t.Errorf("final states: %+v", p)
	}
	if r.Lost() != nil {
		t.Errorf("Lost = %v", r.Lost())
	}
	waitExited(t, h)
}

func TestHostWithoutView(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "exec sleep 30"}})
	viewWait = 200 * time.Millisecond
	t.Cleanup(func() { viewWait = 10 * time.Second })
	h := startTestHost(t, []string{"api"}, hostOptions{forView: true})
	waitExited(t, h) // the runr that started it never attached
}

func TestHostHold(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "echo args:"}})
	h := startTestHost(t, []string{"api"}, hostOptions{hold: true, args: map[string]string{"api": "x"}})
	if st := hostStatus(t, h); len(st.Processes) != 0 {
		t.Fatalf("started while holding: %+v", st.Processes)
	}
	// The view starts it, after the leftovers question.
	r, err := control.Attach(h.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if err := r.Start("api"); err != nil {
		t.Fatal(err)
	}
	waitState(t, h, "api", supervisor.Exited)
	if out := mustRun(t, "logs", "api"); !strings.Contains(out, "args: x") {
		t.Errorf("logs:\n%s", out)
	}
}

func TestHostKilledLeavesLeftovers(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": {Cmd: "exec sleep 30"}})
	h := startTestHost(t, []string{"api"}, hostOptions{})
	waitState(t, h, "api", supervisor.Running)
	p, _ := findProcess(hostStatus(t, h), "api")
	t.Cleanup(func() { syscall.Kill(-p.Pid, syscall.SIGKILL) })
	syscall.Kill(h.pid, syscall.SIGKILL)
	waitExited(t, h)

	orphans := findLeftovers(sessionsDir())
	if len(orphans) != 1 || len(orphans[0].Children) != 1 || orphans[0].Children[0].Pid != p.Pid {
		t.Fatalf("leftovers: %+v", orphans)
	}
	sessions.Kill(orphans, time.Second)
	if alive(p.Pid) {
		t.Error("api still running after the cleanup")
	}
	// A killed host leaves its log, which the next start removes when
	// it's empty.
	old := time.Now().Add(-time.Hour)
	os.Chtimes(h.log, old, old)
	startTestHost(t, []string{"api"}, hostOptions{hold: true})
	if _, err := os.Stat(h.log); !os.IsNotExist(err) {
		t.Errorf("the killed host's log is still there: %v", err)
	}
}
