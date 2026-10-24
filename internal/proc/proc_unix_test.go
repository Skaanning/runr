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
