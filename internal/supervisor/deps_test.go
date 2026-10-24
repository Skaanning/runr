//go:build !windows

package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
)

// depConfig builds a config from projects given as name → (cmd, deps, ready_log).
type depProject struct {
	cmd      string
	deps     []string
	readyLog string
}

func depSupervisor(t *testing.T, projects map[string]depProject, groups map[string][]string) (*Supervisor, *recorder) {
	t.Helper()
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = testTimeout
	dir := t.TempDir()
	for name, p := range projects {
		cfg.Projects[name] = &config.Project{Dir: dir, Cmd: p.cmd, DependsOn: p.deps, ReadyLog: p.readyLog}
	}
	for name, members := range groups {
		cfg.Groups[name] = members
	}
	if _, err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return newTestSupervisorWithConfig(t, cfg)
}

// fifo creates a named pipe; a command blocks reading it until the test
// writes, which lets tests decide exactly when a process makes progress.
func fifo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	if err := mkfifo(path); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	return path
}

func openGate(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("go\n")
	f.Close()
}

func TestStartPullsInDependencies(t *testing.T) {
	s, r := depSupervisor(t, map[string]depProject{
		"db":  {cmd: "sleep 30"},
		"api": {cmd: "sleep 30", deps: []string{"db"}},
		"web": {cmd: "sleep 30"},
	}, nil)

	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "db", Running)
	r.waitState(t, "api", Running)

	var names []string
	for _, p := range s.Processes() {
		names = append(names, p.Name)
	}
	if !reflect.DeepEqual(names, []string{"db", "api"}) {
		t.Errorf("session = %v (web must not be started)", names)
	}
}

func TestStartGroup(t *testing.T) {
	s, r := depSupervisor(t, map[string]depProject{
		"db":  {cmd: "sleep 30"},
		"api": {cmd: "sleep 30", deps: []string{"db"}},
		"web": {cmd: "sleep 30"},
	}, map[string][]string{"backend": {"api"}, "frontend": {"web"}})

	if err := s.Start("frontend", "backend"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web", "db", "api"} {
		r.waitState(t, name, Running)
	}
	if err := s.Start("nope"); err == nil {
		t.Error("expected error for unknown name")
	}
}

func TestWaitsForReadyLog(t *testing.T) {
	gate := fifo(t)
	s, r := depSupervisor(t, map[string]depProject{
		"db":  {cmd: "echo booting; read x < " + gate + "; printf '\\033[32mready\\033[0m to accept\\n'; sleep 30", readyLog: "ready to accept"},
		"api": {cmd: "echo api up; sleep 30", deps: []string{"db"}},
	}, nil)

	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "db", Starting)
	r.waitState(t, "api", Waiting)
	waitLog(t, s, "db", "out:booting")

	// db is spawned but not ready, so api must still be waiting.
	time.Sleep(100 * time.Millisecond)
	if got := r.states("api"); !reflect.DeepEqual(got, []State{Waiting}) {
		t.Fatalf("api states before db ready = %v", got)
	}

	openGate(t, gate) // db prints its (colored) ready line
	r.waitState(t, "db", Running)
	r.waitState(t, "api", Running)
	waitLog(t, s, "api", "out:api up")

	if got := r.states("db"); !reflect.DeepEqual(got, []State{Starting, Running}) {
		t.Errorf("db states = %v", got)
	}
	if !containsLine(logLines(s, "db"), "sys:ready") {
		t.Errorf("db logs = %q", logLines(s, "db"))
	}
	if !containsLine(logLines(s, "api"), "sys:waiting for db") {
		t.Errorf("api logs = %q", logLines(s, "api"))
	}
}

func TestDiamondStartsInParallelAfterDependency(t *testing.T) {
	gate := fifo(t)
	s, r := depSupervisor(t, map[string]depProject{
		"db":      {cmd: "read x < " + gate + "; echo ready; sleep 30", readyLog: "^ready$"},
		"api":     {cmd: "sleep 30", deps: []string{"db"}},
		"auth":    {cmd: "sleep 30", deps: []string{"db"}},
		"gateway": {cmd: "sleep 30", deps: []string{"api", "auth"}},
	}, nil)

	if err := s.Start("gateway"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "gateway", Waiting)
	openGate(t, gate)
	for _, name := range []string{"db", "api", "auth", "gateway"} {
		r.waitState(t, name, Running)
	}
}

func TestExitedDependencyCountsAsReady(t *testing.T) {
	s, r := depSupervisor(t, map[string]depProject{
		"migrate": {cmd: "echo migrated"},
		"api":     {cmd: "sleep 30", deps: []string{"migrate"}},
	}, nil)

	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "migrate", Exited)
	r.waitState(t, "api", Running)
}

func TestBlockedWhenDependencyFailsBeforeReady(t *testing.T) {
	s, r := depSupervisor(t, map[string]depProject{
		"db":      {cmd: "echo failing; exit 1", readyLog: "ready"},
		"api":     {cmd: "sleep 30", deps: []string{"db"}},
		"gateway": {cmd: "sleep 30", deps: []string{"api"}},
	}, nil)

	if err := s.Start("gateway"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "db", Crashed)
	r.waitState(t, "api", Blocked)
	r.waitState(t, "gateway", Blocked)

	if !containsLine(logLines(s, "api"), "fail:blocked: dependency db is crashed") {
		t.Errorf("api logs = %q", logLines(s, "api"))
	}
	if !containsLine(logLines(s, "gateway"), "fail:blocked: dependency api is blocked") {
		t.Errorf("gateway logs = %q", logLines(s, "gateway"))
	}
}

func TestRestartingDependencyUnblocks(t *testing.T) {
	// db fails the first time and succeeds once the marker file exists.
	marker := filepath.Join(t.TempDir(), "ok")
	s, r := depSupervisor(t, map[string]depProject{
		"db":      {cmd: "test -f " + marker + " || exit 1; echo ready; sleep 30", readyLog: "ready"},
		"api":     {cmd: "sleep 30", deps: []string{"db"}},
		"gateway": {cmd: "sleep 30", deps: []string{"api"}},
	}, nil)

	if err := s.Start("gateway"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "gateway", Blocked)

	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Restart("db"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"db", "api", "gateway"} {
		r.waitState(t, name, Running)
	}
	want := []State{Waiting, Blocked, Waiting, Running}
	if got := r.states("gateway"); !reflect.DeepEqual(got, want) {
		t.Errorf("gateway states = %v, want %v", got, want)
	}
}

func TestDependencyCrashAfterReadyLeavesDependentsRunning(t *testing.T) {
	gate := fifo(t)
	s, r := depSupervisor(t, map[string]depProject{
		"db":  {cmd: "echo ready; read x < " + gate + "; exit 1", readyLog: "ready"},
		"api": {cmd: "sleep 30", deps: []string{"db"}},
	}, nil)

	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "api", Running)
	openGate(t, gate)
	r.waitState(t, "db", Crashed)

	time.Sleep(100 * time.Millisecond)
	if got := r.states("api"); !reflect.DeepEqual(got, []State{Waiting, Running}) {
		t.Errorf("api states = %v", got)
	}
}

func TestStopWaitingProjectCancelsAndBlocksDependents(t *testing.T) {
	gate := fifo(t)
	s, r := depSupervisor(t, map[string]depProject{
		"db":      {cmd: "read x < " + gate + "; echo ready; sleep 30", readyLog: "ready"},
		"api":     {cmd: "sleep 30", deps: []string{"db"}},
		"gateway": {cmd: "sleep 30", deps: []string{"api"}},
	}, nil)

	if err := s.Start("gateway"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "api", Waiting)

	if err := s.Stop("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "api", Stopped)
	r.waitState(t, "gateway", Blocked)
	if !containsLine(logLines(s, "api"), "sys:cancelled") {
		t.Errorf("api logs = %q", logLines(s, "api"))
	}

	// db itself is unaffected.
	openGate(t, gate)
	r.waitState(t, "db", Running)
}

func TestStopAllInReverseDependencyOrder(t *testing.T) {
	// Each project appends to a shared file when it receives SIGTERM, so the
	// file records the order in which they were stopped.
	order := filepath.Join(t.TempDir(), "order")
	stopper := func(name string) string {
		return "trap 'echo " + name + " >> " + order + "; exit 0' TERM; echo ready; while :; do sleep 0.05; done"
	}
	s, r := depSupervisor(t, map[string]depProject{
		"db":      {cmd: stopper("db"), readyLog: "ready"},
		"api":     {cmd: stopper("api"), deps: []string{"db"}, readyLog: "ready"},
		"auth":    {cmd: stopper("auth"), deps: []string{"db"}, readyLog: "ready"},
		"gateway": {cmd: stopper("gateway"), deps: []string{"api", "auth"}, readyLog: "ready"},
	}, nil)

	if err := s.Start("gateway"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"db", "api", "auth", "gateway"} {
		r.waitState(t, name, Running)
	}

	select {
	case <-s.StopAll():
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll did not finish")
	}

	data, _ := os.ReadFile(order)
	lines := strings.Fields(string(data))
	if len(lines) != 4 || lines[0] != "gateway" || lines[3] != "db" {
		t.Errorf("stop order = %v, want gateway, {api, auth}, db", lines)
	}
	for _, p := range s.Processes() {
		if p.State != Stopped {
			t.Errorf("%s is %v after StopAll", p.Name, p.State)
		}
	}
}

func TestStopAllCancelsWaitingAndRejectsStarts(t *testing.T) {
	gate := fifo(t)
	s, r := depSupervisor(t, map[string]depProject{
		"db":  {cmd: "trap '' TERM; echo trapped; read x < " + gate + "; sleep 30", readyLog: "never"},
		"api": {cmd: "sleep 30", deps: []string{"db"}},
	}, nil)

	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "api", Waiting)
	waitLog(t, s, "db", "out:trapped") // SIGTERM must not arrive before the trap is set

	done := s.StopAll()
	r.waitState(t, "api", Stopped)
	r.waitState(t, "db", Stopping)
	if err := s.Start("api"); err != ErrShuttingDown {
		t.Errorf("Start during StopAll: %v", err)
	}

	// db ignores SIGTERM, so it is killed after the timeout.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll did not finish")
	}
	if info := r.waitState(t, "db", Stopped); info.Exit.Signal != "killed" {
		t.Errorf("db exit = %+v", info.Exit)
	}

	// Starting works again after the shutdown completed.
	if err := s.Start("api"); err != nil {
		t.Errorf("Start after StopAll: %v", err)
	}
}

func TestKillAllDuringStopAll(t *testing.T) {
	s, r := depSupervisor(t, map[string]depProject{
		"a": {cmd: "trap '' TERM; echo trapped; sleep 30"},
		"b": {cmd: "trap '' TERM; echo trapped; sleep 30"},
	}, nil)
	s.timeout = time.Hour // only KillAll can end this shutdown in time

	if err := s.Start("a", "b"); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "a", "out:trapped")
	waitLog(t, s, "b", "out:trapped")

	done := s.StopAll()
	r.waitState(t, "a", Stopping)
	s.KillAll()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll did not finish after KillAll")
	}
}

func TestStopAllWithNothingRunning(t *testing.T) {
	s, _ := depSupervisor(t, map[string]depProject{"a": {cmd: "true"}}, nil)
	select {
	case <-s.StopAll():
	case <-time.After(time.Second):
		t.Fatal("StopAll with nothing running did not finish")
	}
}

func TestStripANSI(t *testing.T) {
	tests := map[string]string{
		"plain":                                  "plain",
		"\x1b[32mready\x1b[0m to accept":         "ready to accept",
		"\x1b[1;38;5;208mbold\x1b[m":             "bold",
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x07": "link",
	}
	for in, want := range tests {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCloseKeepsBlockedState(t *testing.T) {
	cfg := config.New()
	dir := t.TempDir()
	cfg.Projects["db"] = &config.Project{Dir: dir, Cmd: "exit 1", ReadyLog: "never"}
	cfg.Projects["api"] = &config.Project{Dir: dir, Cmd: "true", DependsOn: []string{"db"}}
	s := New(cfg, Options{BaseEnv: []string{"PATH=" + os.Getenv("PATH")}})
	r := record(s)

	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "api", Blocked)
	s.Close()
	<-r.closed
	if got := r.states("api"); !reflect.DeepEqual(got, []State{Waiting, Blocked}) {
		t.Errorf("api states = %v (Close must not cancel blocked projects)", got)
	}
}
