package proc

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// helperEnv selects a helper mode: when set, the test binary acts as the
// child process under test instead of running tests.
const helperEnv = "RUNR_PROC_HELPER"

// readyFDEnv tells a helper that fd 3 is a pipe to close once it is set up.
const readyFDEnv = "RUNR_PROC_READY_FD"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		runHelper(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runHelper(mode string) {
	switch mode {
	case "echo":
		fmt.Println("out1")
		fmt.Fprintln(os.Stderr, "err1")
		fmt.Println("out2")
	case "exit3":
		os.Exit(3)
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Println(wd)
	case "env":
		fmt.Println(os.Getenv("RUNR_TEST_VALUE"))
	case "many":
		for i := range 10000 {
			fmt.Println(i)
		}
	case "longline":
		fmt.Println(strings.Repeat("x", 150_000))
	case "noeol":
		fmt.Print("partial")
	case "crlf":
		fmt.Print("a\r\nb\r\n")
	case "badutf8":
		os.Stdout.Write([]byte("a\xffb\n"))
	case "sleep":
		signalReady()
		time.Sleep(time.Hour)
	case "trap":
		// Catch the signals rather than ignore them: on Windows an ignored
		// CTRL_BREAK (os.Interrupt) still ends the process.
		signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM, os.Interrupt)
		signalReady()
		time.Sleep(time.Hour)
	case "slow-graceful": // takes a while to shut down
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM, os.Interrupt)
		fmt.Println("ready")
		<-c
		time.Sleep(200 * time.Millisecond)
		fmt.Println("bye")
	case "graceful":
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM, os.Interrupt)
		fmt.Println("ready")
		<-c
		fmt.Println("bye")
	case "grandchild", "grandchild-trap", "orphan", "orphan-trap":
		// Starts a child sharing stdout ("-trap": one that ignores SIGTERM),
		// then waits ("grandchild") or exits right away ("orphan").
		childMode := "sleep"
		if strings.HasSuffix(mode, "-trap") {
			childMode = "trap"
		}
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"="+childMode)
		child.Stdout = os.Stdout
		var ready *os.File
		if strings.HasPrefix(mode, "orphan") {
			// Don't exit before the child is set up (ExtraFiles is Unix only).
			r, w, err := os.Pipe()
			if err != nil {
				fmt.Println("error", err)
				os.Exit(1)
			}
			ready = r
			child.ExtraFiles = []*os.File{w}
			child.Env = append(child.Env, readyFDEnv+"=1")
		}
		if err := child.Start(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println("child", child.Process.Pid)
		if ready == nil {
			time.Sleep(time.Hour)
		}
		child.ExtraFiles[0].Close()
		_, _ = io.Copy(io.Discard, ready) // EOF once the child closes its end
	default:
		if h := moreHelpers[mode]; h != nil {
			h()
			return
		}
		fmt.Fprintln(os.Stderr, "unknown helper mode", mode)
		os.Exit(2)
	}
}

// moreHelpers are helper modes defined for some platforms only.
var moreHelpers = map[string]func(){}

// signalReady prints "ready" and, when asked to, closes the ready pipe.
func signalReady() {
	fmt.Println("ready")
	if os.Getenv(readyFDEnv) != "" {
		os.NewFile(3, "ready").Close()
	}
}

// collector records output lines and lets tests wait for specific ones.
type collector struct {
	mu    sync.Mutex
	lines []Line
}

func (c *collector) add(l Line) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, l)
}

func (c *collector) texts(stream Stream) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, l := range c.lines {
		if l.Stream == stream {
			out = append(out, l.Text)
		}
	}
	return out
}

// waitForPrefix waits until a stdout line starting with prefix arrives and returns it.
func (c *collector) waitForPrefix(t *testing.T, prefix string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, text := range c.texts(Stdout) {
			if strings.HasPrefix(text, prefix) {
				return text
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for output %q; got %q", prefix, c.texts(Stdout))
	return ""
}

// helperCommand is a shell command line that runs this test binary. On
// Unix the shell execs it, so signals and exit statuses are the helper's:
// bash does that by itself for a single command, dash (/bin/sh on Debian
// and Ubuntu) does not.
func helperCommand() string {
	if runtime.GOOS == "windows" {
		return `"` + os.Args[0] + `"`
	}
	return "exec '" + strings.ReplaceAll(os.Args[0], "'", `'\''`) + "'"
}

// startHelper starts the test binary in the given helper mode.
func startHelper(t *testing.T, mode string, spec Spec) (*Process, *collector) {
	t.Helper()
	spec.Command = helperCommand()
	// GORACE: race-enabled binaries otherwise sleep 1s on exit.
	spec.Env = BuildEnv(append(os.Environ(), spec.Env...), map[string]string{
		helperEnv: mode,
		"GORACE":  "atexit_sleep_ms=0",
	})
	c := &collector{}
	p, err := Start(spec, c.add)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.Kill()
		<-p.Done()
	})
	return p, c
}

// waitDone waits for the process to finish and returns its status.
func waitDone(t *testing.T, p *Process, timeout time.Duration) ExitStatus {
	t.Helper()
	select {
	case <-p.Done():
		return p.Status()
	case <-time.After(timeout):
		t.Fatalf("process did not exit within %v", timeout)
		return ExitStatus{}
	}
}
