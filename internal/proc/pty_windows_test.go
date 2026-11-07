//go:build windows

package proc

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestPTYTerminateWhenCtrlCIgnored checks that Terminate stops a process on
// a pseudo console even though runr ignores Ctrl+C, as it does when run from
// Git Bash: the process must not inherit that.
func TestPTYTerminateWhenCtrlCIgnored(t *testing.T) {
	var was uintptr // restored afterwards: 1 if Ctrl+C was ignored already
	if windows.RtlGetCurrentPeb().ProcessParameters.ConsoleFlags&ctrlCIgnored != 0 {
		was = 1
	}
	if ok, _, err := setConsoleCtrlHandler.Call(0, 1); ok == 0 {
		t.Fatalf("ignoring Ctrl+C: %v", err)
	}
	t.Cleanup(func() { setConsoleCtrlHandler.Call(0, was) })
	// Ignoring Ctrl+C sets the flag withCtrlC reads.
	if windows.RtlGetCurrentPeb().ProcessParameters.ConsoleFlags&ctrlCIgnored == 0 {
		t.Fatal("ConsoleFlags doesn't show that Ctrl+C is ignored")
	}

	up := make(chan struct{})
	var once sync.Once
	p, err := Start(Spec{Command: "echo up& ping -n 60 127.0.0.1 >nul", PTY: true}, func(l Line) {
		if strings.TrimSpace(l.Text) == "up" {
			once.Do(func() { close(up) })
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	select {
	case <-up:
	case <-time.After(10 * time.Second):
		t.Fatal("no output")
	}
	time.Sleep(500 * time.Millisecond) // until ping runs

	start := time.Now()
	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, p, 10*time.Second)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("stopping took %v: Ctrl+C didn't reach it", took)
	}
	if windows.RtlGetCurrentPeb().ProcessParameters.ConsoleFlags&ctrlCIgnored == 0 {
		t.Error("runr's own setting wasn't restored")
	}
}

func TestPTYKillReachesWholeTree(t *testing.T) {
	p, c := startHelper(t, "grandchild", Spec{PTY: true})
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
	waitDone(t, p, 10*time.Second)
	if ev, err := windows.WaitForSingleObject(child, 5000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("grandchild %d still alive after kill (wait result %d, %v)", childPid, ev, err)
	}
}
