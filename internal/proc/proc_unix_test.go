//go:build !windows

package proc

import (
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminate(t *testing.T) {
	p, c := startHelper(t, "sleep", Spec{})
	c.waitForPrefix(t, "ready")

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, p, 5*time.Second)
	if status.Signal != "terminated" || status.Code != -1 {
		t.Errorf("status = %+v", status)
	}
}

func TestTerminateIsGraceful(t *testing.T) {
	p, c := startHelper(t, "graceful", Spec{})
	c.waitForPrefix(t, "ready")

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, p, 5*time.Second)
	if !status.Success() {
		t.Errorf("status = %+v", status)
	}
	c.waitForPrefix(t, "bye")
}

func TestKillAfterTerminateIgnored(t *testing.T) {
	p, c := startHelper(t, "trap", Spec{})
	c.waitForPrefix(t, "ready")

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
		t.Fatal("process exited despite ignoring SIGTERM")
	case <-time.After(300 * time.Millisecond):
	}

	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, p, 5*time.Second)
	if status.Signal != "killed" {
		t.Errorf("status = %+v", status)
	}
}

func TestTerminateReachesWholeGroup(t *testing.T) {
	p, c := startHelper(t, "grandchild", Spec{})
	line := c.waitForPrefix(t, "child ")
	childPid, err := strconv.Atoi(strings.TrimPrefix(line, "child "))
	if err != nil {
		t.Fatalf("bad child line %q", line)
	}

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, p, 5*time.Second)

	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(childPid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return // grandchild is gone
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still alive after group terminate", childPid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSignalAfterExitIsNoop(t *testing.T) {
	p, _ := startHelper(t, "echo", Spec{})
	waitDone(t, p, 5*time.Second)
	if err := p.Terminate(); err != nil {
		t.Errorf("Terminate after exit: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Errorf("Kill after exit: %v", err)
	}
}

// waitGone waits until pid no longer exists.
func waitGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still alive", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func childPid(t *testing.T, c *collector) int {
	t.Helper()
	line := c.waitForPrefix(t, "child ")
	pid, err := strconv.Atoi(strings.TrimPrefix(line, "child "))
	if err != nil {
		t.Fatalf("bad child line %q", line)
	}
	return pid
}

func TestLeftoversStoppedAfterExit(t *testing.T) {
	p, c := startHelper(t, "orphan", Spec{LeftoverTimeout: 5 * time.Second})
	pid := childPid(t, c)

	status := waitDone(t, p, 5*time.Second)
	if !status.Success() || !status.Leftovers {
		t.Errorf("status = %+v, want success with leftovers", status)
	}
	waitGone(t, pid, time.Second)
}

func TestLeftoversKilledAfterTimeout(t *testing.T) {
	start := time.Now()
	p, c := startHelper(t, "orphan-trap", Spec{LeftoverTimeout: 300 * time.Millisecond})
	pid := childPid(t, c)

	status := waitDone(t, p, 5*time.Second)
	if !status.Leftovers {
		t.Errorf("status = %+v, want leftovers", status)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("done after %v, before the leftover timeout", elapsed)
	}
	waitGone(t, pid, time.Second)
}

func TestLeftoversOutliveTerminatedLeader(t *testing.T) {
	// The leader exits on SIGTERM while its child ignores it: Done waits for
	// the child until Kill.
	p, c := startHelper(t, "grandchild-trap", Spec{LeftoverTimeout: time.Hour})
	pid := childPid(t, c)
	c.waitForPrefix(t, "ready")

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
		t.Fatal("done while the child is still running")
	case <-time.After(300 * time.Millisecond):
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, p, 5*time.Second)
	if status.Signal != "terminated" {
		t.Errorf("status = %+v, want the leader's SIGTERM", status)
	}
	waitGone(t, pid, time.Second)
}

func TestNoLeftovers(t *testing.T) {
	p, _ := startHelper(t, "echo", Spec{})
	if status := waitDone(t, p, 5*time.Second); status.Leftovers {
		t.Errorf("status = %+v, want no leftovers", status)
	}
}
