package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/control"
	"github.com/Skaanning/runr/internal/supervisor"
)

// longCmd prints text, then runs for a minute (cmd.exe). ping takes
// Ctrl+C, not Ctrl+Break (it prints its statistics and goes on).
func longCmd(text string) string {
	return "echo " + text + " & ping -n 60 127.0.0.1 >nul"
}

// sleeper is a project that prints text, then runs for a minute: the test
// binary (sleepEnv), which Ctrl+Break ends.
func sleeper(t *testing.T, text string) *config.Project {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &config.Project{Cmd: `"` + exe + `"`, Env: map[string]string{sleepEnv: text}}
}

func TestUpDetachedWindows(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": sleeper(t, "api up")})
	out, _, err := run(t, "up", "-d", "api")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(out, "started session "), " · runr attach to watch it\n"))
	if err != nil {
		t.Fatalf("output %q", out)
	}
	sessions := runningSessions(io.Discard)
	if len(sessions) != 1 || sessions[0].Status.Pid != pid || !sessions[0].Status.Host {
		t.Fatalf("sessions: %+v", sessions)
	}
	h := &host{pid: pid, path: sessions[0].Path, exited: make(chan struct{})}
	waitState(t, h, "api", supervisor.Running)
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(mustRun(t, "logs", "api"), "api up"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("logs:\n%s", mustRun(t, "logs", "api"))
		}
	}
	if out := mustRun(t, "down"); out != "session "+strconv.Itoa(pid)+" stopped\n" {
		t.Errorf("down: %q", out)
	}
	for deadline := time.Now().Add(10 * time.Second); processAlive(pid); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the host is still running after runr down")
		}
	}
	// The log was renamed while the host had it open, and removed at the end.
	dir, _ := hostsDir()
	if logs, _ := filepath.Glob(filepath.Join(dir, "*.log")); len(logs) > 0 {
		t.Errorf("host logs left: %v", logs)
	}
}

func TestHostViewRuleWindows(t *testing.T) {
	hostEnv(t, map[string]*config.Project{"api": sleeper(t, "api up")})
	h := startTestHost(t, []string{"api"}, hostOptions{forView: true})
	waitState(t, h, "api", supervisor.Running)
	r, err := control.Attach(h.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Detach(); err != nil {
		t.Fatal(err)
	}
	stillRunning(t, h)
	if r, err = control.Attach(h.path); err != nil {
		t.Fatal(err)
	}
	r.Drop(errors.New("closed")) // the view's terminal was closed
	waitExited(t, h)
}

// stopsGracefully attaches to a host of the api project, waits for its
// line, stops the session from the view (q) and checks that api stopped
// without being killed.
func stopsGracefully(t *testing.T, p *config.Project) {
	t.Helper()
	hostEnv(t, map[string]*config.Project{"api": p})
	h := startTestHost(t, []string{"api"}, hostOptions{})
	waitState(t, h, "api", supervisor.Running)
	r, err := control.Attach(h.path)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !hasLine(r.Logs(), "api", "api up"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no line from api: %+v", r.Logs().Lines("api"))
		}
	}
	select {
	case <-r.StopAll():
	case <-time.After(20 * time.Second):
		t.Fatal("the session didn't end")
	}
	for _, l := range r.Logs().Lines("api") {
		if strings.Contains(l.Text, "killing") {
			t.Errorf("api was killed: %q", l.Text)
		}
	}
	if ps := r.Processes(); len(ps) != 1 || ps[0].State != supervisor.Stopped {
		t.Errorf("final states: %+v", ps)
	}
	waitExited(t, h)
}

func hasLine(logs *supervisor.Logs, project, substr string) bool {
	for _, l := range logs.Lines(project) {
		if strings.Contains(l.Text, substr) {
			return true
		}
	}
	return false
}

// Ctrl+Break reaches the projects through the host's hidden console.
func TestHostStopsGracefullyWindows(t *testing.T) {
	stopsGracefully(t, sleeper(t, "api up"))
}

// ConPTY works in a host, and Ctrl+C reaches the project through it.
func TestHostPTYWindows(t *testing.T) {
	stopsGracefully(t, &config.Project{Cmd: longCmd("api up"), PTY: true})
}
