//go:build !windows

package proc

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

func init() {
	moreHelpers["size"] = func() {
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		for {
			w, h, err := term.GetSize(os.Stdout.Fd())
			fmt.Println("size", w, h, err)
			<-winch
		}
	}
	moreHelpers["cpr"] = func() {
		// Like programs that ask where the cursor is: raw mode, then the request.
		if _, err := term.MakeRaw(os.Stdin.Fd()); err != nil {
			fmt.Println("error", err)
			return
		}
		fmt.Print("\x1b[6n")
		reply, _ := bufio.NewReader(os.Stdin).ReadString('R')
		fmt.Printf("\ncursor %q\n", reply)
	}
}

// The program on the terminal learns about a resize from SIGWINCH.
func TestPTYResizeSignal(t *testing.T) {
	p, c := startHelper(t, "size", Spec{PTY: true, Cols: 100, Rows: 30})
	c.waitForPrefix(t, "size 100 30 <nil>")
	if err := p.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	c.waitForPrefix(t, "size 80 24 <nil>")
}

func TestPTYCursorRequest(t *testing.T) {
	_, c := startHelper(t, "cpr", Spec{PTY: true})
	c.waitForPrefix(t, `cursor "\x1b[1;1R"`)
}

func TestPTYStopTree(t *testing.T) {
	// The whole tree stops, also what ignores SIGTERM.
	p, c := startHelper(t, "grandchild-trap", Spec{PTY: true, LeftoverTimeout: 300 * time.Millisecond})
	pid := childPid(t, c)
	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, p, 5*time.Second)
	waitGone(t, pid, 2*time.Second)

	// What the command leaves behind is stopped too.
	p, c = startHelper(t, "orphan-trap", Spec{PTY: true, LeftoverTimeout: 300 * time.Millisecond})
	pid = childPid(t, c)
	waitDone(t, p, 5*time.Second)
	waitGone(t, pid, 2*time.Second)
}

func TestPTYProcessGroup(t *testing.T) {
	p, c := startHelper(t, "sleep", Spec{PTY: true})
	c.waitForPrefix(t, "ready")
	if pgid, err := syscall.Getpgid(p.Pid()); err != nil || pgid != p.Pid() {
		t.Errorf("pgid = %d, %v; pid %d", pgid, err, p.Pid())
	}
	if sid, err := unix.Getsid(p.Pid()); err != nil || sid != p.Pid() {
		t.Errorf("sid = %d, %v; pid %d", sid, err, p.Pid())
	}
}
