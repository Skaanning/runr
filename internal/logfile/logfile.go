// Package logfile writes log lines to plain-text files: the files of a
// session's persistent logs (Writer) and the format shared with saved logs
// (Format).
package logfile

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Skaanning/runr/internal/plaintext"
)

// Format formats a log line for a file: its time, the label if there is
// one (e.g. the project, padded to align), "│" before a program's output or
// "»" before runr's own lines, and the text without ANSI escapes.
func Format(t time.Time, label string, runr bool, text string) string {
	var b strings.Builder
	b.WriteString(t.Format("15:04:05.000"))
	b.WriteByte(' ')
	if label != "" {
		b.WriteString(label)
		b.WriteByte(' ')
	}
	if runr {
		b.WriteString("» ")
	} else {
		b.WriteString("│ ")
	}
	b.WriteString(plaintext.StripANSI(text))
	return b.String()
}

// Header is the line that starts a session in a project's file.
func Header(t time.Time) string {
	return "--- runr session " + t.Format("2006-01-02 15:04:05") + " ---"
}

// flushEvery is how often a Writer flushes its buffers. A variable so tests
// can shorten it.
var flushEvery = time.Second

// Writer appends each project's lines to <dir>/<project>.log, buffered and
// flushed at least every second and on Close. A project's file is opened,
// and the session's header written, with its first line. After an error a
// project's lines are dropped; onError is told once.
type Writer struct {
	dir     string
	onError func(project, path string, err error)

	mu     sync.Mutex
	files  map[string]*file // by project; nil once writing to it failed
	closed bool
	stop   chan struct{}
	done   chan struct{}
}

type file struct {
	path string
	f    *os.File
	w    *bufio.Writer
}

// NewWriter returns a Writer for dir, which must exist. onError is called
// with the Writer's lock held, so it must not call the Writer or block.
func NewWriter(dir string, onError func(project, path string, err error)) *Writer {
	w := &Writer{dir: dir, onError: onError, files: map[string]*file{},
		stop: make(chan struct{}), done: make(chan struct{})}
	go w.flusher()
	return w
}

// Write appends a line to the project's file (see Format).
func (w *Writer) Write(project string, t time.Time, runr bool, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	f, ok := w.files[project]
	if !ok {
		f = w.open(project, t)
	}
	if f == nil {
		return
	}
	if _, err := f.w.WriteString(Format(t, "", runr, text) + "\n"); err != nil {
		w.fail(project, f, err)
	}
}

// open opens a project's file for appending and writes the header.
func (w *Writer) open(project string, t time.Time) *file {
	path := filepath.Join(w.dir, project+".log")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		w.files[project] = nil
		w.onError(project, path, err)
		return nil
	}
	lf := &file{path: path, f: f, w: bufio.NewWriterSize(f, 64*1024)}
	w.files[project] = lf
	if _, err := lf.w.WriteString(Header(t) + "\n"); err != nil {
		w.fail(project, lf, err)
		return nil
	}
	return lf
}

// fail stops writing a project's file after err.
func (w *Writer) fail(project string, f *file, err error) {
	f.f.Close()
	w.files[project] = nil
	w.onError(project, f.path, err)
}

func (w *Writer) flusher() {
	defer close(w.done)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			w.mu.Lock()
			w.flushAll()
			w.mu.Unlock()
		case <-w.stop:
			return
		}
	}
}

func (w *Writer) flushAll() {
	for project, f := range w.files {
		if f == nil {
			continue
		}
		if err := f.w.Flush(); err != nil {
			w.fail(project, f, err)
		}
	}
}

// Close flushes and closes the files. Lines written afterwards are dropped.
func (w *Writer) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.flushAll()
	for _, f := range w.files {
		if f != nil {
			f.f.Close()
		}
	}
	w.mu.Unlock()
	close(w.stop)
	<-w.done
}
