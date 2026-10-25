//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/sessions"
)

// recordEnv makes the test binary act like a runr session that records one
// child process in the given session directory, and then waits to be
// killed.
const recordEnv = "RUNR_TEST_RECORD"

func TestMain(m *testing.M) {
	if os.Getenv(fakeDockerEnv) != "" {
		fakeDocker()
	}
	if dir := os.Getenv(recordEnv); dir != "" {
		rec, err := sessions.Open(dir)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		child := exec.Command("sleep", "30")
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := child.Start(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		rec.Spawned("api", child.Process.Pid, "sleep 30")
		fmt.Println("child", child.Process.Pid)
		time.Sleep(time.Hour)
	}
	os.Exit(m.Run())
}

// killedSession runs a recording session, kills it with SIGKILL, and
// returns the pid of the child it left behind.
func killedSession(t *testing.T) int {
	t.Helper()
	dir, err := sessions.Dir()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), recordEnv+"="+dir)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "child ")))
	if err != nil {
		t.Fatalf("helper said %q", line)
	}
	t.Cleanup(func() { syscall.Kill(-pid, syscall.SIGKILL) })
	cmd.Process.Kill()
	cmd.Wait()
	return pid
}

// stateEnv keeps session files in a fresh directory.
func stateEnv(t *testing.T) {
	t.Helper()
	testEnv(t)                    // XDG_STATE_HOME on Linux
	t.Setenv("HOME", t.TempDir()) // macOS: ~/Library/Caches
}

func alive(pid int) bool {
	return syscall.Kill(-pid, 0) == nil
}

func TestCleanup(t *testing.T) {
	stateEnv(t)
	if out := mustRun(t, "cleanup"); out != "no processes left over from earlier runr sessions\n" {
		t.Errorf("nothing: %q", out)
	}

	pid := killedSession(t)
	// Declining leaves it running, and it is still found later.
	root := newRootCmd()
	var stdout strings.Builder
	root.SetArgs([]string{"cleanup"})
	root.SetIn(strings.NewReader("n\n"))
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("api  pid %d  sleep 30\nkill them? [y/N] left them running\n", pid)
	if stdout.String() != want || !alive(pid) {
		t.Errorf("declined: %q, want %q", stdout.String(), want)
	}

	start := time.Now()
	out := mustRun(t, "cleanup", "--yes")
	if !strings.HasSuffix(out, "stopped 1 leftover process\n") {
		t.Errorf("--yes: %q", out)
	}
	for deadline := time.Now().Add(2 * time.Second); alive(pid) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if alive(pid) {
		t.Error("the leftover still runs")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("cleanup took %v", d)
	}
	if out := mustRun(t, "cleanup"); !strings.HasPrefix(out, "no processes") {
		t.Errorf("after cleanup: %q", out)
	}
}

func TestKillReport(t *testing.T) {
	two := []sessions.Orphan{{Children: []sessions.Child{{Project: "api"}, {Project: "db"}}}}
	if got := killReport(two, nil, time.Second); got != "stopped 2 leftover processes" {
		t.Errorf("got %q", got)
	}
	if got := killReport(two, []sessions.Child{{Project: "db"}}, 5*time.Second); got != "stopped 2 leftover processes (killed db after 5s)" {
		t.Errorf("got %q", got)
	}
}
