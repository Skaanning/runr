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

func (q *eventQueue) pump() {
	for range q.notify {
		for {
			q.mu.Lock()
			items, closed := q.items, q.closed
			q.items = nil
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

// maxLag is how many events a subscriber may fall behind before it is
// dropped. A variable so tests can shorten it.
var maxLag = 1 << 22

// eventHub delivers every event to the session's consumer (Events) and to
// its subscribers (Subscribe), each through its own queue, in the same
// order. It remembers the pending lines for new subscribers.
type eventHub struct {
	main *eventQueue

	mu      sync.Mutex
	subs    map[*Subscription]bool
	pending map[string]string // the latest PendingEvent text by project
	closed  bool
}

func newEventHub() *eventHub {
	return &eventHub{main: newEventQueue(), subs: map[*Subscription]bool{}, pending: map[string]string{}}
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
	for sub := range h.subs {
		if sub.q.size() >= maxLag {
			sub.lagged.Store(true)
			sub.q.discard()
			delete(h.subs, sub)
			continue
		}
		sub.q.push(e)
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
	q      *eventQueue
	hub    *eventHub
	lagged atomic.Bool
}

// Events delivers every event after the subscription began. It is closed
// by Close, when the supervisor closes, and when the subscriber fell too
// far behind (see Lagged).
func (s *Subscription) Events() <-chan Event { return s.q.out }

// Lagged reports whether the subscription was dropped for falling too far
// behind.
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
