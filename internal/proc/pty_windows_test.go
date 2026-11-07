//go:build windows

package proc

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

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
