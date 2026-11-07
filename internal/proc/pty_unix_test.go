//go:build !windows

package proc

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

func init() {
	moreHelpers["tty"] = func() {
		// Like programs that color only on a terminal.
		if term.IsTerminal(os.Stdout.Fd()) {
			fmt.Println("\x1b[32mterminal\x1b[0m")
		} else {
			fmt.Println("pipe")
		}
		fmt.Fprintln(os.Stderr, "to stderr")
		fmt.Println(os.Getenv("TERM"))
	}
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

func TestPTYOutput(t *testing.T) {
	p, c := startHelper(t, "tty", Spec{})
	if status := waitDone(t, p, 5*time.Second); !status.Success() {
		t.Fatalf("pipes: %+v", status)
	}
	if out, errs := c.texts(Stdout), c.texts(Stderr); !slices.Equal(out[:1], []string{"pipe"}) || !slices.Equal(errs, []string{"to stderr"}) {
		t.Errorf("pipes: stdout %q, stderr %q", out, errs)
	}

	// On a terminal: colors, and one stream, all read by Done.
	p, c = startHelper(t, "tty", Spec{PTY: true, Env: []string{"TERM=xterm-256color"}})
	if status := waitDone(t, p, 5*time.Second); !status.Success() {
		t.Fatalf("pty: %+v", status)
	}
	if out, errs := c.texts(Stdout), c.texts(Stderr); !slices.Equal(out, []string{"\x1b[32mterminal\x1b[0m", "to stderr", "xterm-256color"}) || len(errs) != 0 {
		t.Errorf("pty: stdout %q, stderr %q", out, errs)
	}
}

func TestPTYSize(t *testing.T) {
	_, c := startHelper(t, "size", Spec{PTY: true})
	c.waitForPrefix(t, fmt.Sprintf("size %d %d <nil>", DefaultCols, DefaultRows))

	p, c := startHelper(t, "size", Spec{PTY: true, Cols: 100, Rows: 30})
	c.waitForPrefix(t, "size 100 30 <nil>")
	if err := p.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	c.waitForPrefix(t, "size 80 24 <nil>")

	_ = p.Kill()
	waitDone(t, p, 5*time.Second)
	if err := p.Resize(90, 24); err != nil { // after the end: nothing
		t.Errorf("Resize after exit: %v", err)
	}
	pipes, _ := startHelper(t, "sleep", Spec{})
	if err := pipes.Resize(90, 24); err != nil {
		t.Errorf("Resize without a terminal: %v", err)
	}
}

func TestPTYCursorRequest(t *testing.T) {
	_, c := startHelper(t, "cpr", Spec{PTY: true})
	c.waitForPrefix(t, `cursor "\x1b[1;1R"`)
}

func TestPTYStop(t *testing.T) {
	p, c := startHelper(t, "graceful", Spec{PTY: true})
	c.waitForPrefix(t, "ready")
	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	if status := waitDone(t, p, 5*time.Second); !status.Success() {
		t.Errorf("status = %+v", status)
	}
	c.waitForPrefix(t, "bye")

	// The whole tree stops, also what ignores SIGTERM.
	p, c = startHelper(t, "grandchild-trap", Spec{PTY: true, LeftoverTimeout: 300 * time.Millisecond})
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
