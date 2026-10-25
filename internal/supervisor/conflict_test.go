//go:build !windows

package supervisor

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
)

// listenEnv makes the test binary listen on the given port and wait, as
// the process of a project.
const listenEnv = "RUNR_SUPERVISOR_LISTEN"

func TestMain(m *testing.M) {
	if port := os.Getenv(listenEnv); port != "" {
		l, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		defer l.Close()
		fmt.Println("listening")
		time.Sleep(time.Hour)
	}
	os.Exit(m.Run())
}

func TestLocalURLPort(t *testing.T) {
	for raw, want := range map[string]int{
		"http://localhost:8080/health": 8080,
		"http://127.0.0.1/":            80,
		"https://[::1]/x":              443,
		"http://127.0.0.2:3000":        3000,
		"http://example.com:8080/":     0, // not this machine
		"http://192.168.1.5:8080/":     0,
		"":                             0,
		"::":                           0,
	} {
		if got := localURLPort(raw); got != want {
			t.Errorf("localURLPort(%q) = %d, want %d", raw, got, want)
		}
	}
}

// takenPort listens on a free port of 127.0.0.1 for the rest of the test.
func takenPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// waitWarning waits for a project's log to contain a system line with
// prefix and returns it.
func waitWarning(t *testing.T, s *Supervisor, name, prefix string) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		for _, l := range logLines(s, name) {
			if strings.HasPrefix(l, "sys:"+prefix) {
				return strings.TrimPrefix(l, "sys:")
			}
		}
	}
	t.Fatalf("no warning %q; logs %q", prefix, logLines(s, name))
	return ""
}

func TestPortConflictWarns(t *testing.T) {
	port := takenPort(t)
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = testTimeout
	cfg.Projects["api"] = &config.Project{Dir: t.TempDir(), Cmd: "sleep 30", ReadyPort: port}
	s, r := newTestSupervisorWithConfig(t, cfg)
	if err := s.Start("api"); err != nil {
		t.Fatal(err)
	}
	// The owner is this test, found by pid; it is started anyway.
	want := fmt.Sprintf("port %d is in use by pid %d (supervisor.test)", port, os.Getpid())
	if got := waitWarning(t, s, "api", "port "); got != want {
		t.Errorf("warning = %q, want %q", got, want)
	}
	r.waitState(t, "api", Running)
	// Events arrive through a queue, after the log line is stored.
	got := r.warningsOf("api")
	for deadline := time.Now().Add(5 * time.Second); len(got) == 0 && time.Now().Before(deadline); got = r.warningsOf("api") {
		time.Sleep(5 * time.Millisecond)
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("warning events = %q", got)
	}
}

func TestPortConflictSeenPorts(t *testing.T) {
	port := takenPort(t)
	s, r := newTestSupervisor(t, map[string]string{"web": "sleep 30", "other": "sleep 30"})
	if err := s.Start("web", "other"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "web", Running)
	r.waitState(t, "other", Running)

	// Seen while web ran (by the TUI); then something else takes the port.
	s.RememberPorts(map[string][]int{"web": {port}, "nope": {1}})
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "web", Stopped)
	if got := r.warningsOf("web"); len(got) != 0 {
		t.Errorf("warned before the port was known: %q", got)
	}
	if err := s.Start("web"); err != nil {
		t.Fatal(err)
	}
	waitWarning(t, s, "web", "port "+strconv.Itoa(port)+" is in use by pid ")
	// Ports seen for one project don't affect the others.
	if err := s.Restart("other"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "other", Running)
	time.Sleep(2 * conflictTimeout)
	if got := r.warningsOf("other"); len(got) != 0 {
		t.Errorf("other warned: %q", got)
	}
}

func TestPortConflictNotForOwnPort(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0") // a free port for the project
	free := l.Addr().(*net.TCPAddr).Port
	l.Close()

	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = testTimeout
	cmd := "exec '" + strings.ReplaceAll(os.Args[0], "'", `'\''`) + "'"
	cfg.Projects["own"] = &config.Project{Dir: t.TempDir(), Cmd: cmd, ReadyPort: free,
		Env: map[string]string{listenEnv: strconv.Itoa(free)}}
	cfg.Projects["quiet"] = &config.Project{Dir: t.TempDir(), Cmd: "sleep 30"} // no known ports
	s, r := newTestSupervisorWithConfig(t, cfg)
	if err := s.Start("own", "quiet"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "own", Running)
	// Restarting finds the port taken by the run being replaced at most,
	// which is gone by the time the new run starts.
	if err := s.Restart("own"); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "own", "sys:ready")
	time.Sleep(2 * conflictTimeout)
	if got := r.warningsOf("own"); len(got) != 0 {
		t.Errorf("own port reported as taken: %q", got)
	}
	if got := r.warningsOf("quiet"); len(got) != 0 {
		t.Errorf("quiet warned: %q", got)
	}
}
