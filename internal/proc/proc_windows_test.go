//go:build windows

package proc

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestTerminate(t *testing.T) {
	p, c := startHelper(t, "sleep", Spec{})
	c.waitForPrefix(t, "ready")

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	if status := waitDone(t, p, 5*time.Second); status.Success() {
		t.Errorf("status = %+v, want failure after CTRL_BREAK", status)
	}
}

func TestTerminateIsGraceful(t *testing.T) {
	p, c := startHelper(t, "graceful", Spec{})
	c.waitForPrefix(t, "ready")

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	if status := waitDone(t, p, 5*time.Second); !status.Success() {
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
		t.Fatal("process exited despite ignoring CTRL_BREAK")
	case <-time.After(300 * time.Millisecond):
	}

	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	if status := waitDone(t, p, 5*time.Second); status.Success() {
		t.Errorf("status = %+v", status)
	}
}

func TestKillReachesWholeTree(t *testing.T) {
	p, c := startHelper(t, "grandchild", Spec{})
	line := c.waitForPrefix(t, "child ")
	childPid, err := strconv.Atoi(strings.TrimPrefix(line, "child "))
	if err != nil {
		t.Fatalf("bad child line %q", line)
	}

	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPid))
	if err != nil {
		t.Fatalf("opening grandchild: %v", err)
	}
	defer windows.CloseHandle(child)

	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, p, 5*time.Second)

	if ev, err := windows.WaitForSingleObject(child, 5000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("grandchild %d still alive after kill (wait result %d, %v)", childPid, ev, err)
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
