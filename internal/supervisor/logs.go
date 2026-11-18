package supervisor

import (
	"slices"
	"sync"
	"time"
)

// Stream identifies where a log line came from.
type Stream int

const (
	Stdout Stream = iota
	Stderr
	System      // lines written by runr itself, e.g. "starting: npm run dev"
	SystemError // runr's lines about failures, e.g. "crashed (exit code 1)"
)

// Runr reports whether the line was written by runr itself.
func (s Stream) Runr() bool { return s == System || s == SystemError }

// LogLine is one line of a project's output.
type LogLine struct {
	Seq     uint64 // global, strictly increasing across all projects
	Project string
	Stream  Stream
	Text    string
	Time    time.Time
	Error   bool // a program line matching settings.error_pattern, or runr's "crashed" / "failed to start"
}

// Logs stores the most recent output lines of every project in per-project
// ring buffers. It is safe for concurrent use.
type Logs struct {
	mu       sync.Mutex
	capacity int
	seq      uint64
	buffers  map[string]*ring
	now      func() time.Time
	onAppend func(LogLine) // called with every line, in order, under mu; nil: none
	changed  chan struct{} // closed by the next Append; nil until Changed asks
}

// NewLogs returns a store keeping at most capacity lines per project.
func NewLogs(capacity int) *Logs {
	return &Logs{
		capacity: max(capacity, 1),
		buffers:  map[string]*ring{},
		now:      time.Now,
	}
}

// Append stores a line and returns it with its sequence number and timestamp.
func (l *Logs) Append(project string, stream Stream, text string) LogLine {
	return l.AppendLine(LogLine{Project: project, Stream: stream, Text: text})
}

// AppendLine stores line, setting its sequence number and timestamp, and
// returns it.
func (l *Logs) AppendLine(line LogLine) LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	line.Seq, line.Time = l.seq, l.now()
	buf := l.buffers[line.Project]
	if buf == nil {
		buf = &ring{capacity: l.capacity}
		l.buffers[line.Project] = buf
	}
	buf.push(line)
	if l.onAppend != nil {
		l.onAppend(line)
	}
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
	return line
}

// Insert stores a line that already has its sequence number and time, e.g.
// one of another process's session (a view's copy of its log). Lines of a
// project must be inserted in order.
func (l *Logs) Insert(line LogLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq = max(l.seq, line.Seq)
	buf := l.buffers[line.Project]
	if buf == nil {
		buf = &ring{capacity: l.capacity}
		l.buffers[line.Project] = buf
	}
	buf.push(line)
	if l.onAppend != nil {
		l.onAppend(line)
	}
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
}

// Changed returns a channel that is closed when the next line is stored,
// for followers that read the new lines with Since.
func (l *Logs) Changed() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changed == nil {
		l.changed = make(chan struct{})
	}
	return l.changed
}

// Since returns the buffered lines after sequence number seq of the given
// projects (all projects if none are given), interleaved in arrival order.
func (l *Logs) Since(seq uint64, projects ...string) []LogLine {
	l.mu.Lock()
	var parts [][]LogLine
	for name, buf := range l.buffers {
		if len(projects) == 0 || slices.Contains(projects, name) {
			if p := buf.after(seq); len(p) > 0 {
				parts = append(parts, p)
			}
		}
	}
	l.mu.Unlock()
	return merge(parts)
}

// Lines returns a copy of a project's buffered lines, oldest first.
func (l *Logs) Lines(project string) []LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	if buf := l.buffers[project]; buf != nil {
		return buf.snapshot()
	}
	return nil
}

// Combined returns the buffered lines of the given projects (all projects if
// none are given) interleaved in arrival order.
func (l *Logs) Combined(projects ...string) []LogLine {
	l.mu.Lock()
	var parts [][]LogLine
	if len(projects) == 0 {
		for _, buf := range l.buffers {
			parts = append(parts, buf.snapshot())
		}
	} else {
		for _, name := range projects {
			if buf := l.buffers[name]; buf != nil {
				parts = append(parts, buf.snapshot())
			}
		}
	}
	l.mu.Unlock()
	return merge(parts)
}

// merge interleaves parts, each sorted by Seq, into one slice sorted by Seq.
func merge(parts [][]LogLine) []LogLine {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	// Repeatedly take the smallest head. The number of projects is small, so
	// a linear scan is fine.
	out := make([]LogLine, 0, total)
	for len(out) < total {
		best := -1
		for i, p := range parts {
			if len(p) > 0 && (best == -1 || p[0].Seq < parts[best][0].Seq) {
				best = i
			}
		}
		out = append(out, parts[best][0])
		parts[best] = parts[best][1:]
	}
	return out
}

// ring is a fixed-capacity FIFO of log lines. Its storage grows on demand up
// to capacity, so projects with little output stay cheap.
type ring struct {
	capacity int
	lines    []LogLine
	start    int // index of the oldest line once the buffer is full
	n        int
}

func (r *ring) push(line LogLine) {
	if r.n < r.capacity {
		r.lines = append(r.lines, line)
		r.n++
		return
	}
	r.lines[r.start] = line
	r.start = (r.start + 1) % r.capacity
}

// after returns a copy of the lines with a sequence number above seq,
// oldest first.
func (r *ring) after(seq uint64) []LogLine {
	k := 0 // lines after seq, counted from the newest
	for k < r.n && r.lines[(r.start+r.n-1-k)%r.capacity].Seq > seq {
		k++
	}
	out := make([]LogLine, k)
	for i := range k {
		out[i] = r.lines[(r.start+r.n-k+i)%r.capacity]
	}
	return out
}

func (r *ring) snapshot() []LogLine {
	out := make([]LogLine, r.n)
	k := copy(out, r.lines[r.start:])
	copy(out[k:], r.lines[:r.start])
	return out
}
