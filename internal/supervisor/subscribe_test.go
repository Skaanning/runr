//go:build !windows

package supervisor

import (
	"testing"
	"time"
)

// subLines collects the texts of a subscription's line events until one
// equals last.
func subLines(t *testing.T, sub *Subscription, last string) []string {
	t.Helper()
	var out []string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("closed after %q", out)
			}
			if l, ok := e.(LineEvent); ok {
				out = append(out, l.Line.Text)
				if l.Line.Text == last {
					return out
				}
			}
		case <-timeout:
			t.Fatalf("%q never arrived; got %q", last, out)
		}
	}
}

func TestSubscribe(t *testing.T) {
	s, r := newTestSupervisor(t, map[string]string{"api": "echo one; echo two; echo three"})
	a, pending := s.Subscribe()
	b, _ := s.Subscribe()
	if len(pending) != 0 {
		t.Errorf("pending = %v", pending)
	}
	s.Start("api")
	r.waitState(t, "api", Exited)
	want := []string{"starting: echo one; echo two; echo three", "one", "two", "three"}
	for _, sub := range []*Subscription{a, b} {
		got := subLines(t, sub, "three")
		if len(got) != len(want) {
			t.Fatalf("lines = %q", got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("lines = %q", got)
			}
		}
	}
	a.Close()
	a.Close() // twice is fine
	if _, ok := <-a.Events(); ok {
		// events queued before Close may still come; the channel must close
		for range a.Events() {
		}
	}
	s.Close()
	for range b.Events() { // closed with the supervisor
	}
	c, _ := s.Subscribe()
	if _, ok := <-c.Events(); ok {
		t.Error("a subscription after Close is open")
	}
}

func TestSubscribePending(t *testing.T) {
	s, _ := newTestSupervisor(t, map[string]string{})
	s.events.push(PendingEvent{Project: "api", Text: "branch: "})
	s.events.push(PendingEvent{Project: "db", Text: "x"})
	s.events.push(PendingEvent{Project: "db"}) // finished
	_, pending := s.Subscribe()
	if len(pending) != 1 || pending["api"] != "branch: " {
		t.Errorf("pending = %v", pending)
	}
}

func TestSubscriberDroppedWhenBehind(t *testing.T) {
	oldCompact, oldLag := compactAt, maxLag
	compactAt, maxLag = 10, 10 // warnings can't be compacted
	t.Cleanup(func() { compactAt, maxLag = oldCompact, oldLag })
	s, r := newTestSupervisor(t, map[string]string{})
	slow, _ := s.Subscribe()
	for range 1000 {
		s.events.push(WarningEvent{Project: "api", Text: "x"})
	}
	deadline := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-slow.Events():
		case <-deadline:
			t.Fatal("the slow subscriber was not dropped")
		}
	}
	if !slow.Lagged() {
		t.Error("Lagged = false")
	}
	// The session's own consumer gets everything.
	deadline = time.After(5 * time.Second)
	for len(r.warningsOf("api")) < 1000 {
		select {
		case <-deadline:
			t.Fatalf("Events got %d of 1000", len(r.warningsOf("api")))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func line(project, text string) LineEvent {
	return LineEvent{Line: LogLine{Project: project, Text: text}}
}

func state(name string, st State, errors int) StateEvent {
	return StateEvent{Info: ProcessInfo{Name: name, State: st, Errors: errors}}
}

func TestCompact(t *testing.T) {
	q := &eventQueue{items: []Event{
		state("api", Starting, 0),
		line("api", "a1"),
		line("db", "d1"),
		PendingEvent{Project: "api", Text: "br"},
		line("api", "a2"),
		state("api", Running, 0),
		state("api", Running, 1), // superseded by the next one
		state("api", Running, 2),
		line("db", "d2"),
		PendingEvent{Project: "api", Text: "branch"},
		line("api", "a3"),
		WarningEvent{Project: "db", Text: "port taken"},
		line("api", "a4"),
		state("api", Crashed, 2),
	}}
	// Two lines per project, three in all.
	if n := q.compact(2, 3); n != len(q.items) {
		t.Errorf("compact returned %d, %d left", n, len(q.items))
	}
	want := []Event{
		state("api", Starting, 0),
		line("db", "d1"),
		state("api", Running, 2),
		line("db", "d2"),
		PendingEvent{Project: "api", Text: "branch"},
		line("api", "a3"),
		WarningEvent{Project: "db", Text: "port taken"},
		line("api", "a4"),
		state("api", Crashed, 2),
	}
	if len(q.items) != len(want) {
		t.Fatalf("got %d events: %+v", len(q.items), q.items)
	}
	for i := range want {
		if q.items[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, q.items[i], want[i])
		}
	}
}

// A subscriber that is far behind gets the newest lines, not all of them,
// and isn't dropped.
func TestSlowSubscriberCompacted(t *testing.T) {
	oldCompact, oldLag := compactAt, maxLag
	compactAt, maxLag = 100, 1000
	t.Cleanup(func() { compactAt, maxLag = oldCompact, oldLag })
	s, _ := newTestSupervisor(t, map[string]string{})
	s.events.scrollback = 50
	slow, _ := s.Subscribe()
	for i := range 100_000 {
		s.events.push(LineEvent{Line: LogLine{Project: "api", Seq: uint64(i + 1)}})
	}
	if n := slow.q.size(); n > 2*compactAt+pumpChunk+256 {
		t.Errorf("backlog of %d events", n)
	}
	var last uint64
	got := 0
	deadline := time.After(5 * time.Second)
	for last < 100_000 {
		select {
		case e, open := <-slow.Events():
			if !open {
				t.Fatal("the slow subscriber was dropped")
			}
			l := e.(LineEvent).Line
			if l.Seq <= last {
				t.Fatalf("seq %d after %d", l.Seq, last)
			}
			last = l.Seq
			got++
		case <-deadline:
			t.Fatalf("got up to seq %d", last)
		}
	}
	if got >= 100_000 {
		t.Errorf("got every line (%d)", got)
	}
	if slow.Lagged() {
		t.Error("Lagged")
	}
}
