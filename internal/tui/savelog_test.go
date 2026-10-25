package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

// pressSave presses w and handles the result.
func pressSave(t *testing.T, m *runningModel) {
	t.Helper()
	_, cmd := m.Update(key("w"))
	if cmd == nil {
		t.Fatal("w returned no command")
	}
	m.Update(cmd())
}

// readSaved returns a saved log with the times replaced by T.
func readSaved(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d\.\d{3} `).ReplaceAllString(string(b), "T ")
}

func TestSaveLog(t *testing.T) {
	m, sess := newTestModel(t, 100, 12)
	m.saveDir = t.TempDir()
	logs := sess.Logs()
	logs.Append("db", supervisor.System, "starting: run-db")
	logs.Append("worker", supervisor.Stdout, "\x1b[31mpanic\x1b[0m: missing QUEUE_URL")
	logs.Append("db", supervisor.Stdout, "ready")
	logs.Append("worker", supervisor.SystemError, "crashed (exit code 1)")

	// Combined: every line, with the project's label.
	pressSave(t, m)
	path := filepath.Join(m.saveDir, "runr-combined-"+testNow().Format("20060102-150405")+".log")
	want := "T db     » starting: run-db\n" +
		"T Worker │ panic: missing QUEUE_URL\n" +
		"T db     │ ready\n" +
		"T Worker » crashed (exit code 1)\n"
	if got := readSaved(t, path); got != want {
		t.Errorf("combined:\n%s\nwant:\n%s", got, want)
	}
	if m.notice != "saved 4 lines to "+config.DisplayPath(path) {
		t.Errorf("notice = %q", m.notice)
	}
	if got := savedNotice(savedLogMsg{path: "runr-combined-20261025-132758.log", lines: 26}); got != "saved 26 lines to runr-combined-20261025-132758.log" {
		t.Errorf("relative notice = %q", got)
	}

	// One project's view: its lines only.
	m.Update(key("down"))
	m.Update(key("down"))
	m.Update(key("tab"))
	pressSave(t, m)
	path = filepath.Join(m.saveDir, "runr-worker-"+testNow().Format("20060102-150405")+".log")
	if got, want := readSaved(t, path), "T │ panic: missing QUEUE_URL\nT » crashed (exit code 1)\n"; got != want {
		t.Errorf("worker:\n%s\nwant:\n%s", got, want)
	}
}

func TestSaveLogFails(t *testing.T) {
	m, _ := newTestModel(t, 100, 12)
	m.saveDir = filepath.Join(t.TempDir(), "missing")
	pressSave(t, m)
	if !strings.HasPrefix(m.notice, "can't save the log: open ") {
		t.Errorf("notice = %q", m.notice)
	}
}
