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
	old := maxLag
	maxLag = 10
	t.Cleanup(func() { maxLag = old })
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
