package supervisor

import "sync"

// Event is sent on Supervisor.Events: a StateEvent, LineEvent or
// WarningEvent.
type Event interface {
	isEvent()
}

// StateEvent reports that a project changed state.
type StateEvent struct {
	Info ProcessInfo
}

// LineEvent reports a new log line (also stored in Supervisor.Logs).
type LineEvent struct {
	Line LogLine
}

// WarningEvent reports a problem worth pointing out that doesn't stop the
// project, e.g. that a port it uses is taken. Its text is also in the log.
type WarningEvent struct {
	Project string
	Text    string
}

func (StateEvent) isEvent()   {}
func (LineEvent) isEvent()    {}
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
