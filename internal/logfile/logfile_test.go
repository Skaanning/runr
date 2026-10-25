package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 25, 13, 5, 12, 345_000_000, time.Local)

func TestFormat(t *testing.T) {
	for _, tt := range []struct {
		label string
		runr  bool
		text  string
		want  string
	}{
		{"", false, "\x1b[32mGET\x1b[0m /users", "13:05:12.345 │ GET /users"},
		{"", true, "starting: npm run dev", "13:05:12.345 » starting: npm run dev"},
		{"api   ", false, "hello", "13:05:12.345 api    │ hello"},
		{"db    ", true, "ready", "13:05:12.345 db     » ready"},
	} {
		if got := Format(t0, tt.label, tt.runr, tt.text); got != tt.want {
			t.Errorf("Format(%q, %v, %q) = %q, want %q", tt.label, tt.runr, tt.text, got, tt.want)
		}
	}
}

// errs records the errors a Writer reports.
type errs struct {
	mu   sync.Mutex
	list []string
}

func (e *errs) add(project, path string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, project+": "+filepath.Base(path))
}

func (e *errs) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.list...)
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriterAppendsSessions(t *testing.T) {
	dir := t.TempDir()
	e := &errs{}
	for i := range 2 {
		w := NewWriter(dir, e.add)
		start := t0.Add(time.Duration(i) * time.Hour)
		w.Write("api", start, true, "starting: go run .")
		w.Write("api", start, false, "\x1b[1mlistening\x1b[0m on :8080")
		w.Write("db", start, false, "ready")
		w.Close()
		w.Write("api", start, false, "after close") // dropped
	}
	want := "--- runr session 2026-10-25 13:05:12 ---\n" +
		"13:05:12.345 » starting: go run .\n" +
		"13:05:12.345 │ listening on :8080\n" +
		"--- runr session 2026-10-25 14:05:12 ---\n" +
		"14:05:12.345 » starting: go run .\n" +
		"14:05:12.345 │ listening on :8080\n"
	if got := read(t, filepath.Join(dir, "api.log")); got != want {
		t.Errorf("api.log:\n%s\nwant:\n%s", got, want)
	}
	if got := read(t, filepath.Join(dir, "db.log")); strings.Count(got, "--- runr session") != 2 {
		t.Errorf("db.log:\n%s", got)
	}
	if len(e.get()) != 0 {
		t.Errorf("errors: %q", e.get())
	}
}

func TestWriterFlushesWhileRunning(t *testing.T) {
	old := flushEvery
	flushEvery = 10 * time.Millisecond
	t.Cleanup(func() { flushEvery = old })

	dir := t.TempDir()
	w := NewWriter(dir, (&errs{}).add)
	defer w.Close()
	w.Write("api", t0, false, "before the crash")
	path := filepath.Join(dir, "api.log")
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if b, _ := os.ReadFile(path); strings.Contains(string(b), "before the crash") {
			return
		}
	}
	t.Fatalf("line not flushed; file %q", read(t, path))
}

func TestWriterOpenError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "api.log"), 0o755); err != nil { // can't be opened as a file
		t.Fatal(err)
	}
	e := &errs{}
	w := NewWriter(dir, e.add)
	w.Write("api", t0, false, "one")
	w.Write("api", t0, false, "two")
	w.Write("db", t0, false, "fine")
	w.Close()
	if got := e.get(); len(got) != 1 || got[0] != "api: api.log" {
		t.Errorf("errors = %q, want one for api", got)
	}
	if got := read(t, filepath.Join(dir, "db.log")); !strings.Contains(got, "fine") {
		t.Errorf("db.log = %q", got)
	}
}

func TestWriterWriteError(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	dir := t.TempDir()
	if err := os.Symlink("/dev/full", filepath.Join(dir, "api.log")); err != nil {
		t.Skip(err)
	}
	e := &errs{}
	w := NewWriter(dir, e.add)
	for range 3 {
		w.Write("api", t0, false, strings.Repeat("x", 40_000)) // fills the buffer: written at once
	}
	w.Close()
	if got := e.get(); len(got) != 1 || got[0] != "api: api.log" {
		t.Errorf("errors = %q, want one for api", got)
	}
}
