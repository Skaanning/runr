//go:build windows

package supervisor

import (
	"os"
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
		t.Errorf("stopping took %v: Ctrl+C didn't reach it", took)
	}
	for _, l := range s.Logs().Lines("term") {
		if strings.ContainsRune(sgr.ReplaceAllString(l.Text, ""), '\x1b') {
			t.Errorf("an escape sequence other than colors in %q", l.Text)
		}
	}
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)
