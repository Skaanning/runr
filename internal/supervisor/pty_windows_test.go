//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
)

// The other supervisor tests use a POSIX shell; this one runs cmd.exe
// commands on a pseudo console (ConPTY).
func TestPTYWindows(t *testing.T) {
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = 10 * time.Second
	cfg.Projects["term"] = &config.Project{Dir: t.TempDir(), PTY: true, ReadyLog: `^ready$`,
		Cmd: `echo ready& echo TERM=%TERM%& ping -n 60 127.0.0.1 >nul`}
	s := New(cfg, Options{BaseEnv: os.Environ()})
	t.Cleanup(s.Close)

	var mu sync.Mutex
	var states []State
	go func() {
		for e := range s.Events() {
			if se, ok := e.(StateEvent); ok {
				mu.Lock()
				states = append(states, se.Info.State)
				mu.Unlock()
			}
		}
	}()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("no %s; states %v, log %q", what, states, texts(s.Logs().Lines("term")))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	reached := func(want State) func() bool {
		return func() bool {
			mu.Lock()
			defer mu.Unlock()
			return slices.Contains(states, want)
		}
	}
	logged := func(text string) func() bool {
		return func() bool {
			return slices.ContainsFunc(s.Logs().Lines("term"), func(l LogLine) bool { return l.Stream == Stdout && l.Text == text })
		}
	}

	if err := s.Start("term"); err != nil {
		t.Fatal(err)
	}
	waitFor("running (ready_log)", reached(Running))
	waitFor("TERM line", logged("TERM=xterm-256color"))

	start := time.Now()
	if err := s.Stop("term"); err != nil {
		t.Fatal(err)
	}
	waitFor("stopped", reached(Stopped))
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("stopping took %v: Ctrl+C didn't reach it; log %q", took, texts(s.Logs().Lines("term")))
	}
	for _, l := range s.Logs().Lines("term") {
		if strings.ContainsRune(sgr.ReplaceAllString(l.Text, ""), '\x1b') {
			t.Errorf("an escape sequence other than colors in %q", l.Text)
		}
	}
}

// TestInputWindows answers a PowerShell Read-Host prompt through Input.
func TestInputWindows(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed")
	}
	cfg := config.New()
	cfg.Projects["ask"] = &config.Project{Dir: t.TempDir(), PTY: true,
		Cmd: `pwsh -NoLogo -NoProfile -Command "$b = Read-Host 'branch'; Write-Output ('got ' + $b); Start-Sleep 30"`}
	s := New(cfg, Options{BaseEnv: os.Environ()})
	t.Cleanup(s.Close)

	var mu sync.Mutex
	var pending []string
	go func() {
		for e := range s.Events() {
			if pe, ok := e.(PendingEvent); ok {
				mu.Lock()
				pending = append(pending, pe.Text)
				mu.Unlock()
			}
		}
	}()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("no %s; pending %q, log %q", what, pending, texts(s.Logs().Lines("ask")))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if err := s.Start("ask"); err != nil {
		t.Fatal(err)
	}
	waitFor("prompt", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(pending, func(p string) bool { return strings.Contains(sgr.ReplaceAllString(p, ""), "branch:") })
	})
	for _, b := range []string{"feature/xx", "\x7f", "\r"} {
		if err := s.Input("ask", []byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("answer", func() bool {
		return slices.ContainsFunc(s.Logs().Lines("ask"), func(l LogLine) bool { return sgr.ReplaceAllString(l.Text, "") == "got feature/x" })
	})
	t.Logf("pending %q\nlog %q", pending, texts(s.Logs().Lines("ask")))
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)
