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

// testSession is a plain session running in the background.
type testSession struct {
	t      *testing.T
	out    *syncBuffer
	sigs   chan os.Signal
	result chan error
	err    error
	ended  bool
}

// end waits for the session to end and returns its result.
func (s *testSession) end() error {
	s.t.Helper()
	if !s.ended {
		s.ended = true
		s.err = waitResult(s.t, s.result)
	}
	return s.err
}

// stop stops the session as Ctrl+C does, unless it has ended.
func (s *testSession) stop() {
	s.t.Helper()
	if !s.ended {
		s.sigs <- os.Interrupt
		s.end()
	}
}

// startSessionWith registers projects and runs a plain session of names,
// and waits until other commands can reach it.
func startSessionWith(t *testing.T, projects map[string]*config.Project, names ...string) *testSession {
	t.Helper()
	cfg := plainConfig(t, projects)
	controlEnv(t) // after plainConfig, which sets its own state directory
	// The commands read the config for groups.
	path, _ := config.Path()
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	out, sigs, result := startPlain(t, cfg, names...)
	s := &testSession{t: t, out: out, sigs: sigs, result: result}
	t.Cleanup(s.stop)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, err := run(t, "status"); err == nil {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startSession runs a plain session of two long-running projects.
func startSession(t *testing.T) (stop func()) {
	t.Helper()
	s := startSessionWith(t, map[string]*config.Project{
		"db":  {Cmd: "echo db up; echo 'ERROR boom'; exec sleep 30", DisplayName: "Database"},
		"api": {Cmd: "echo api up; exec sleep 30"},
	}, "db", "api")
	waitOutput(t, s.out, "db up")
	waitOutput(t, s.out, "api up")
	return s.stop
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

func TestActionCommands(t *testing.T) {
	old := pollEvery
	pollEvery = 5 * time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	sess := startSessionWith(t, map[string]*config.Project{
		"db":   {Cmd: "exec sleep 30"},
		"api":  {Cmd: "echo api up; exec sleep 30", DependsOn: []string{"db"}},
		"bad":  {Cmd: "echo oops; exit 2"},
		"slow": {Cmd: "exec sleep 30", ReadyLog: "never"},
	}, "api")
	waitOutput(t, sess.out, "api up")

	out := mustRun(t, "restart", "api", "--wait")
	if !strings.HasPrefix(out, "api running (pid ") {
		t.Errorf("restart --wait: %q", out)
	}
	if out := mustRun(t, "status", "--json"); !strings.Contains(out, `"restarts": 1`) {
		t.Errorf("not restarted:\n%s", out)
	}

	// start adds projects to the session, and reports a crash with its lines.
	out, _, err := run(t, "start", "bad", "--wait")
	wantErr(t, err, "bad crashed (exit code 2)")
	if !strings.Contains(out, "--- last lines of bad ---\n") || !strings.Contains(out, " │ oops\n") {
		t.Errorf("start bad:\n%s", out)
	}

	_, _, err = run(t, "start", "slow", "--wait", "--timeout", "200ms")
	wantErr(t, err, "slow still starting after 200ms")

	if out := mustRun(t, "stop", "api", "slow", "--wait"); !strings.Contains(out, "api stopped (") || !strings.Contains(out, "slow stopped (") {
		t.Errorf("stop --wait:\n%s", out)
	}
	mustRun(t, "restart", "api") // without --wait: returns at once
	_, _, err = run(t, "stop", "nope")
	wantErr(t, err, "not in the running session: nope")
	_, _, err = run(t, "start", "nope")
	wantErr(t, err, `unknown project or group "nope"`)
}

func TestDownCommand(t *testing.T) {
	sess := startSessionWith(t, map[string]*config.Project{
		"db": {Cmd: "echo up; exec sleep 30"},
	}, "db")
	waitOutput(t, sess.out, "up")
	if out := mustRun(t, "down"); out != "session "+strconv.Itoa(os.Getpid())+" stopped\n" {
		t.Errorf("down: %q", out)
	}
	if err := sess.end(); err != nil {
		t.Errorf("session: %v", err)
	}
	if !strings.Contains(sess.out.String(), "stopped (signal: terminated)") {
		t.Errorf("not stopped gracefully:\n%s", sess.out)
	}
	_, _, err := run(t, "down")
	wantErr(t, err, "no runr session is running")
}

func TestDownForce(t *testing.T) {
	sess := startSessionWith(t, map[string]*config.Project{
		"stubborn": {Cmd: "trap '' TERM; echo up; while :; do sleep 0.05; done"},
	}, "stubborn")
	waitOutput(t, sess.out, "up")
	start := time.Now()
	mustRun(t, "down", "--force")
	sess.end()
	if d := time.Since(start); d > 3*time.Second { // the shutdown timeout is 5s
		t.Errorf("down --force took %v", d)
	}
}

func TestQuitter(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	quit := quitter(sigs)
	quit(false)
	quit(false) // a second graceful down doesn't force
	if len(sigs) != 1 {
		t.Fatalf("%d signals", len(sigs))
	}
	quit(true)
	quit(true)
	if len(sigs) != 2 {
		t.Fatalf("%d signals after force", len(sigs))
	}
}
