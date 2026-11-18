//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/control"
	"github.com/Skaanning/runr/internal/state"
)

// controlEnv is testEnv with a state directory short enough for sockets.
func controlEnv(t *testing.T) {
	t.Helper()
	testEnv(t)
	dir, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv(state.EnvVar, dir)
}

// startSession runs a plain session of two long-running projects and waits
// until other commands can reach it.
func startSession(t *testing.T) (stop func()) {
	t.Helper()
	cfg := plainConfig(t, map[string]*config.Project{
		"db":  {Cmd: "echo db up; echo 'ERROR boom'; exec sleep 30", DisplayName: "Database"},
		"api": {Cmd: "echo api up; exec sleep 30"},
	})
	controlEnv(t) // after plainConfig, which sets its own state directory
	out, sigs, result := startPlain(t, cfg, "db", "api")
	waitOutput(t, out, "db up")
	waitOutput(t, out, "api up")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, err := run(t, "status"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopped := false
	stop = func() {
		if !stopped {
			stopped = true
			sigs <- os.Interrupt
			waitResult(t, result)
		}
	}
	t.Cleanup(stop)
	return stop
}

func TestStatusCommand(t *testing.T) {
	stop := startSession(t)
	pid := strconv.Itoa(os.Getpid())

	out := mustRun(t, "status")
	if !strings.HasPrefix(out, "session "+pid+" · db, api · started ") {
		t.Errorf("header:\n%s", out)
	}
	for _, want := range []string{"NAME  STATE", "\ndb    running  ", "\napi   running  "} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}

	out = mustRun(t, "status", "--json", "--session", pid)
	var sessions []control.Status
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if len(sessions) != 1 || len(sessions[0].Processes) != 2 {
		t.Fatalf("json: %+v", sessions)
	}
	if db := sessions[0].Processes[0]; db.Name != "db" || db.Label != "Database" || db.State != "running" || db.Pid == 0 || db.Errors != 1 {
		t.Errorf("db: %+v", db)
	}

	_, _, err := run(t, "status", "--session", "1")
	wantErr(t, err, "no runr session with pid 1 is running")

	stop()
	_, _, err = run(t, "status")
	wantErr(t, err, "no runr session is running")
	_, _, err = run(t, "logs")
	wantErr(t, err, "no runr session is running")
}

func TestLogsCommand(t *testing.T) {
	startSession(t)

	// One project: no labels.
	out := mustRun(t, "logs", "db")
	if !strings.Contains(out, " │ db up\n") || !strings.Contains(out, " » starting: echo db up") || strings.Contains(out, "api") {
		t.Errorf("logs db:\n%s", out)
	}
	// Several: labels padded to the longest, the display name.
	out = mustRun(t, "logs")
	if !strings.Contains(out, " Database │ db up\n") || !strings.Contains(out, " api      │ api up\n") {
		t.Errorf("logs:\n%s", out)
	}
	if out := mustRun(t, "logs", "--errors"); strings.Count(out, "\n") != 1 || !strings.Contains(out, "│ ERROR boom") {
		t.Errorf("logs --errors:\n%s", out)
	}
	if out := mustRun(t, "logs", "-n", "1", "db"); strings.Count(out, "\n") != 1 {
		t.Errorf("logs -n 1:\n%s", out)
	}

	_, _, err := run(t, "logs", "web")
	wantErr(t, err, "not in the running session: web")
	_, _, err = run(t, "logs", "-n", "-1")
	wantErr(t, err, "--lines can't be negative")
}

func TestLogsFollowCommand(t *testing.T) {
	stop := startSession(t)

	out, errOut := &syncBuffer{}, &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		root := newRootCmd()
		root.SetArgs([]string{"logs", "-f", "api"})
		root.SetOut(out)
		root.SetErr(errOut)
		done <- root.Execute()
	}()
	waitOutput(t, out, "│ api up")
	stop() // the lines about stopping arrive, then the follow ends
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("logs -f did not end with the session")
	}
	if !strings.Contains(out.String(), "» stopped") || !strings.Contains(errOut.String(), "the session ended") {
		t.Errorf("out:\n%s\nerr:\n%s", out, errOut)
	}
}
