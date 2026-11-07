package supervisor

import (
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
	Error   bool // the program's line matches settings.error_pattern
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
	return line
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
	total := 0
	if len(projects) == 0 {
		for _, buf := range l.buffers {
			parts = append(parts, buf.snapshot())
			total += buf.n
		}
	} else {
		for _, name := range projects {
			if buf := l.buffers[name]; buf != nil {
				parts = append(parts, buf.snapshot())
				total += buf.n
			}
		}
	}
	l.mu.Unlock()

	// Each part is already sorted by Seq; merge by repeatedly taking the
	// smallest head. The number of projects is small, so a linear scan is fine.
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

func (r *ring) snapshot() []LogLine {
	out := make([]LogLine, r.n)
	k := copy(out, r.lines[r.start:])
	copy(out[k:], r.lines[:r.start])
	return out
}
