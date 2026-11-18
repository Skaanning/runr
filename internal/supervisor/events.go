package supervisor

import (
	"sync"
	"sync/atomic"
)

// Event is sent on Supervisor.Events: a StateEvent, LineEvent,
// PendingEvent or WarningEvent.
type Event interface {
	isEvent()
}

// StateEvent reports that a project changed state, or that its count of
// error lines (ProcessInfo.Errors) went up.
type StateEvent struct {
	Info ProcessInfo
}

// LineEvent reports a new log line (also stored in Supervisor.Logs).
type LineEvent struct {
	Line LogLine
}

// PendingEvent reports the unfinished current line of a project that runs
// on a pseudo-terminal (pty = true) as it changes, e.g. a prompt and what is
// typed after it. It is not a log line: once the line is complete it arrives
// as a LineEvent, after a PendingEvent with an empty Text.
type PendingEvent struct {
	Project string
	Text    string
}

// WarningEvent reports a problem worth pointing out that doesn't stop the
// project, e.g. that a port it uses is taken. Its text is also in the log.
type WarningEvent struct {
	Project string
	Text    string
}

func (StateEvent) isEvent()   {}
func (LineEvent) isEvent()    {}
func (PendingEvent) isEvent() {}
func (WarningEvent) isEvent() {}

// eventQueue decouples producers from the consumer: push never blocks, and a
// pump goroutine delivers events in order on out.
type eventQueue struct {
	mu     sync.Mutex
	items  []Event
	closed bool
	notify chan struct{}
	out    chan Event
}

func newEventQueue() *eventQueue {
	q := &eventQueue{notify: make(chan struct{}, 1), out: make(chan Event, 256)}
	go q.pump()
	return q
}

func (q *eventQueue) push(e Event) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, e)
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// close delivers the remaining events, then closes out.
func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// pumpChunk is how many events the pump takes from the queue at a time:
// the rest stays in items, where size counts it and compact can thin it.
const pumpChunk = 1024

func (q *eventQueue) pump() {
	for range q.notify {
		for {
			q.mu.Lock()
			n := min(len(q.items), pumpChunk)
			items, closed := q.items[:n:n], q.closed
			if n == len(q.items) {
				q.items = nil
			} else {
				q.items = q.items[n:]
			}
			q.mu.Unlock()

			for _, e := range items {
				q.out <- e
			}
			if closed && len(items) == 0 {
				close(q.out)
				return
			}
			if len(items) == 0 {
				break
			}
		}
	}
}

// compact thins out the undelivered events to what a view keeps anyway:
// the newest perProject lines of each project, or the newest total lines
// of all (the combined log), the newest pending line of a project, and the
// last of a project's state events in a row with the same state (e.g.
// error counts going up). It returns how many events are left.
func (q *eventQueue) compact(perProject, total int) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	drop := make([]bool, len(items))
	lines := 0
	byProject := map[string]int{}
	pending := map[string]bool{}
	states := map[string]State{} // the state of a project's next state event
	for i := len(items) - 1; i >= 0; i-- {
		switch e := items[i].(type) {
		case LineEvent:
			p := e.Line.Project
			drop[i] = lines >= total && byProject[p] >= perProject
			lines++
			byProject[p]++
		case PendingEvent:
			drop[i] = pending[e.Project]
			pending[e.Project] = true
		case StateEvent:
			next, ok := states[e.Info.Name]
			drop[i] = ok && next == e.Info.State
			states[e.Info.Name] = e.Info.State
		}
	}
	kept := items[:0]
	for i, e := range items {
		if !drop[i] {
			kept = append(kept, e)
		}
	}
	clear(items[len(kept):]) // for the garbage collector
	q.items = kept
	return len(kept)
}

// discard drops the undelivered events and closes out.
func (q *eventQueue) discard() {
	q.mu.Lock()
	q.items, q.closed = nil, true
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// size is the number of undelivered events.
func (q *eventQueue) size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Subscribers that fall behind: a variable so tests can shorten them.
var (
	// compactAt is how many events a subscriber may fall behind before
	// its backlog is compacted (eventQueue.compact).
	compactAt = 1 << 16
	// maxLag is how many events a subscriber may still be behind after
	// that before it is dropped.
	maxLag = 1 << 22
)

// eventHub delivers every event to the session's consumer (Events) and to
// its subscribers (Subscribe), each through its own queue, in the same
// order. It remembers the pending lines for new subscribers. A subscriber
// that falls behind gets its backlog compacted to what a view keeps of the
// logs: scrollback lines per project (see Settings.ScrollbackLines).
type eventHub struct {
	main       *eventQueue
	scrollback int

	mu       sync.Mutex
	subs     map[*Subscription]bool
	pending  map[string]string // the latest PendingEvent text by project
	projects map[string]bool   // that had events
	closed   bool
}

func newEventHub(scrollback int) *eventHub {
	return &eventHub{
		main:       newEventQueue(),
		scrollback: scrollback,
		subs:       map[*Subscription]bool{},
		pending:    map[string]string{},
		projects:   map[string]bool{},
	}
}

func (h *eventHub) push(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.main.push(e)
	if p, ok := e.(PendingEvent); ok {
		if p.Text == "" {
			delete(h.pending, p.Project)
		} else {
			h.pending[p.Project] = p.Text
		}
	}
	switch e := e.(type) {
	case StateEvent:
		h.projects[e.Info.Name] = true
	case LineEvent:
		h.projects[e.Line.Project] = true
	}
	for sub := range h.subs {
		sub.q.push(e)
		n := sub.q.size()
		if n < max(sub.compactAt, compactAt) {
			continue
		}
		// The combined log keeps scrollback lines per project in all.
		n = sub.q.compact(h.scrollback, h.scrollback*len(h.projects))
		sub.compactAt = 2 * n // so that compacting stays cheap
		if n >= maxLag {
			sub.lagged.Store(true)
			sub.q.discard()
			delete(h.subs, sub)
		}
	}
}

func (h *eventHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	h.main.close()
	for sub := range h.subs {
		sub.q.close()
		delete(h.subs, sub)
	}
}

// Subscription is a stream of a session's events besides Events, e.g. for
// a view attached from another terminal (see Supervisor.Subscribe).
type Subscription struct {
	q         *eventQueue
	hub       *eventHub
	lagged    atomic.Bool
	compactAt int // the backlog to compact at next, if more than compactAt
}

// Events delivers every event after the subscription began, except that a
// subscriber more than compactAt events behind skips what a view wouldn't
// keep (older lines beyond the scrollback, superseded pending lines and
// states). It is closed by Close, when the supervisor closes, and when the
// subscriber fell too far behind even so (see Lagged).
func (s *Subscription) Events() <-chan Event { return s.q.out }

// Lagged reports whether the subscription was dropped for falling too far
// behind: its backlog didn't fit in maxLag events even after compacting.
func (s *Subscription) Lagged() bool { return s.lagged.Load() }

// Close ends the subscription; undelivered events are dropped.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if s.hub.subs[s] {
		delete(s.hub.subs, s)
		s.q.discard()
	}
}
