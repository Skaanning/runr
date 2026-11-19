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

// resizesLive: a running process keeps its size (see conPTY.resize).
const resizesLive = false

// TestPTYResizeNoRepaint: resizing doesn't put the pseudo console's screen
// into the output, neither right after the start (blank lines, as a user
// saw: runr resizes when a view lays out or attaches) nor at a prompt
// (the screen's lines again).
func TestPTYResizeNoRepaint(t *testing.T) {
	for _, early := range []bool{true, false} {
		var mu sync.Mutex
		var lines []string
		prompt, done := make(chan struct{}), make(chan struct{})
		var once, finished sync.Once
		p, err := Start(Spec{Command: "echo hello& set /p x=name: & echo done", PTY: true}, func(l Line) {
			mu.Lock()
			defer mu.Unlock()
			if l.Partial {
				if strings.Contains(l.Text, "name:") {
					once.Do(func() { close(prompt) })
				}
				return
			}
			lines = append(lines, l.Text)
			if strings.TrimSpace(l.Text) == "done" {
				finished.Do(func() { close(done) })
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if early {
			p.Resize(90, 50)
		}
		select {
		case <-prompt:
		case <-time.After(15 * time.Second):
			t.Fatal("no prompt")
		}
		if !early {
			p.Resize(90, 50)
			time.Sleep(700 * time.Millisecond) // a repaint would come now
		}
		if _, err := p.Write([]byte("feature\r")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("no end")
		}
		waitDone(t, p, 10*time.Second)
		mu.Lock()
		got := make([]string, len(lines))
		for i, l := range lines {
			got[i] = strings.TrimSpace(consoleLine(l))
		}
		mu.Unlock()
		if want := []string{"hello", "name: feature", "done"}; strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("early = %v: lines %q, want %q", early, got, want)
		}
	}
}
