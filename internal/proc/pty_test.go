package proc

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Skaanning/runr/internal/plaintext"
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
	moreHelpers["sizepoll"] = func() {
		// Prints the terminal's size whenever it changes.
		last := ""
		for {
			w, h, err := term.GetSize(os.Stdout.Fd())
			if now := fmt.Sprint("size ", w, " ", h, " ", err); now != last {
				fmt.Println(now)
				last = now
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	moreHelpers["ask"] = func() {
		// A prompt waiting for a line of input.
		fmt.Print("branch: ")
		answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Printf("got %s (%v)\n", strings.TrimRight(answer, "\r\n"), err)
	}
	moreHelpers["wideline"] = func() {
		fmt.Println(strings.Repeat("x", 300))
		fmt.Println("after")
	}
}

// nonEmpty returns the lines that aren't empty.
func nonEmpty(lines []string) []string {
	return slices.DeleteFunc(slices.Clone(lines), func(s string) bool { return s == "" })
}

func TestPTYOutput(t *testing.T) {
	p, c := startHelper(t, "tty", Spec{})
	if status := waitDone(t, p, 5*time.Second); !status.Success() {
		t.Fatalf("pipes: %+v", status)
	}
	if out, errs := c.texts(Stdout), c.texts(Stderr); len(out) == 0 || out[0] != "pipe" || !slices.Equal(errs, []string{"to stderr"}) {
		t.Errorf("pipes: stdout %q, stderr %q", out, errs)
	}

	// On a terminal: colors, and one stream, all read by Done.
	p, c = startHelper(t, "tty", Spec{PTY: true, Env: []string{"TERM=xterm-256color"}})
	if status := waitDone(t, p, 10*time.Second); !status.Success() {
		t.Fatalf("pty: %+v; output %q", status, c.texts(Stdout))
	}
	out := nonEmpty(c.texts(Stdout))
	if len(out) != 3 || !strings.Contains(out[0], "\x1b[") || plaintext.StripANSI(out[0]) != "terminal" ||
		out[1] != "to stderr" || out[2] != "xterm-256color" || len(c.texts(Stderr)) != 0 {
		t.Errorf("pty: stdout %q, stderr %q", c.texts(Stdout), c.texts(Stderr))
	}
}

func TestPTYSize(t *testing.T) {
	_, c := startHelper(t, "sizepoll", Spec{PTY: true})
	c.waitForPrefix(t, fmt.Sprintf("size %d %d <nil>", DefaultCols, DefaultRows))

	p, c := startHelper(t, "sizepoll", Spec{PTY: true, Cols: 100, Rows: 30})
	c.waitForPrefix(t, "size 100 30 <nil>")
	if err := p.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	if resizesLive {
		c.waitForPrefix(t, "size 80 24 <nil>")
	}

	_ = p.Kill()
	waitDone(t, p, 10*time.Second)
	if err := p.Resize(90, 24); err != nil { // after the end: nothing
		t.Errorf("Resize after exit: %v", err)
	}
	pipes, _ := startHelper(t, "sleep", Spec{})
	if err := pipes.Resize(90, 24); err != nil {
		t.Errorf("Resize without a terminal: %v", err)
	}
}

// A line longer than the terminal is wide stays one line.
func TestPTYLongLine(t *testing.T) {
	p, c := startHelper(t, "wideline", Spec{PTY: true, Cols: 80})
	waitDone(t, p, 10*time.Second)
	if out := nonEmpty(c.texts(Stdout)); !slices.Equal(out, []string{strings.Repeat("x", 300), "after"}) {
		t.Errorf("output %q", c.texts(Stdout))
	}
}

func TestPTYTerminateIsGraceful(t *testing.T) {
	p, c := startHelper(t, "graceful", Spec{PTY: true})
	c.waitForPrefix(t, "ready")
	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	if status := waitDone(t, p, 10*time.Second); !status.Success() {
		t.Errorf("status = %+v; output %q", status, c.texts(Stdout))
	}
	c.waitForPrefix(t, "bye")
}

func TestPTYInput(t *testing.T) {
	p, c := startHelper(t, "ask", Spec{PTY: true})
	deadline := time.Now().Add(10 * time.Second)
	for !slices.ContainsFunc(c.partials(), func(s string) bool {
		return plaintext.StripANSI(s) == "branch: " || plaintext.StripANSI(s) == "branch:"
	}) {
		if time.Now().After(deadline) {
			t.Fatalf("no prompt; partial lines %q, lines %q", c.partials(), c.texts(Stdout))
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Typed with a mistake corrected by backspace (DEL, as terminals send it).
	if _, err := p.Write([]byte("feature/xx\x7f\r")); err != nil {
		t.Fatal(err)
	}
	if status := waitDone(t, p, 10*time.Second); !status.Success() {
		t.Fatalf("status %+v; output %q", status, c.texts(Stdout))
	}
	out := nonEmpty(c.texts(Stdout))
	for i := range out {
		out[i] = plaintext.StripANSI(out[i])
	}
	if !slices.Contains(out, "branch: feature/x") || !slices.Contains(out, "got feature/x (<nil>)") {
		t.Errorf("output %q", out)
	}
	if _, err := p.Write([]byte("x")); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("write after exit: %v", err)
	}
}

func TestWriteWithoutTerminal(t *testing.T) {
	p, _ := startHelper(t, "tty", Spec{})
	if _, err := p.Write([]byte("x")); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("err = %v", err)
	}
	waitDone(t, p, 5*time.Second)
}
