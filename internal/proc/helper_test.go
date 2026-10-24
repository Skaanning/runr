package proc

import (
	"fmt"
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
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "trap":
		signal.Ignore(syscall.SIGTERM, os.Interrupt) // CTRL_BREAK arrives as os.Interrupt on Windows
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "graceful":
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM, os.Interrupt)
		fmt.Println("ready")
		<-c
		fmt.Println("bye")
	case "grandchild":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := child.Start(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println("child", child.Process.Pid)
		time.Sleep(time.Hour)
	default:
		fmt.Fprintln(os.Stderr, "unknown helper mode", mode)
		os.Exit(2)
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

// helperCommand is a shell command line that runs this test binary.
func helperCommand() string {
	if runtime.GOOS == "windows" {
		return `"` + os.Args[0] + `"`
	}
	return "'" + strings.ReplaceAll(os.Args[0], "'", `'\''`) + "'"
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
