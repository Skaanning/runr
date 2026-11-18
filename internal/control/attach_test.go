//go:build !windows

package control

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

// liveSession runs a supervisor with the projects given as name → command
// (a command starting with "pty:" runs on a terminal), started, its own
// events consumed as a session's owner does, and serves it. quit, if set,
// is the session's way of stopping.
func liveSession(t *testing.T, projects map[string]string, quit func(bool)) (*supervisor.Supervisor, *Server) {
	t.Helper()
	return liveSessionWith(t, projects, Info{Quit: quit})
}

// liveSessionWith is liveSession with more of the Info.
func liveSessionWith(t *testing.T, projects map[string]string, info Info) (*supervisor.Supervisor, *Server) {
	t.Helper()
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = 2 * time.Second
	dir := t.TempDir()
	var names []string
	for name, cmd := range projects {
		rest, pty := strings.CutPrefix(cmd, "pty:")
		cfg.Projects[name] = &config.Project{Dir: dir, Cmd: rest, PTY: pty, DisplayName: strings.ToUpper(name)}
		names = append(names, name)
	}
	slices.Sort(names)
	sup := supervisor.New(cfg, supervisor.Options{BaseEnv: []string{"PATH=" + os.Getenv("PATH")}})
	go func() {
		for range sup.Events() {
		}
	}()
	t.Cleanup(sup.Close)
	if err := sup.Start(names...); err != nil {
		t.Fatal(err)
	}
	info.Pid, info.Titles, info.Config = 42, names, cfg
	srv, err := Listen(shortDir(t), sup, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return sup, srv
}

// waitFor reads a remote's events until ok accepts one.
func waitFor(t *testing.T, r *Remote, what string, ok func(supervisor.Event) bool) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, open := <-r.Events():
			if !open {
				t.Fatalf("events closed before %s", what)
			}
			if ok(e) {
				return
			}
		case <-timeout:
			t.Fatalf("no %s", what)
		}
	}
}

func lineIs(text string) func(supervisor.Event) bool {
	return func(e supervisor.Event) bool {
		l, ok := e.(supervisor.LineEvent)
		return ok && l.Line.Text == text
	}
}

func waitLogLine(t *testing.T, logs *supervisor.Logs, name, text string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		for _, l := range logs.Lines(name) {
			if l.Text == text {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never logged %q", name, text)
		}
	}
}

func TestAttach(t *testing.T) {
	sup, srv := liveSession(t, map[string]string{"api": "echo one; echo two; exec sleep 30"}, nil)
	waitLogLine(t, sup.Logs(), "api", "two")

	r, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	snap := r.Snapshot()
	if snap.Pid != 42 || !slices.Equal(snap.Titles, []string{"api"}) || snap.Config.Projects["api"].DisplayName != "API" {
		t.Errorf("snapshot = %+v", snap)
	}
	if ps := r.Processes(); len(ps) != 1 || ps[0].Name != "api" || ps[0].State != supervisor.Running || ps[0].Pid == 0 {
		t.Errorf("processes = %+v", ps)
	}
	// The buffered lines come first, as events and in the copy of the log.
	var first []string
	for len(first) < 3 {
		e := <-r.Events()
		if l, ok := e.(supervisor.LineEvent); ok {
			first = append(first, l.Line.Text)
		}
	}
	if !slices.Equal(first, []string{"starting: echo one; echo two; exec sleep 30", "one", "two"}) {
		t.Errorf("first lines = %q", first)
	}
	if lines := r.Logs().Lines("api"); len(lines) != 3 || lines[2].Seq != sup.Logs().Lines("api")[2].Seq {
		t.Errorf("logs = %+v", lines)
	}

	// Commands act on the session; its events follow.
	if err := r.Note("api", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, r, "the note", lineIs("hello"))
	waitLogLine(t, r.Logs(), "api", "hello")
	if pids := r.Pids(); len(pids["api"]) == 0 {
		t.Errorf("pids = %v", pids)
	}
	if err := r.Restart("api"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, r, "the restart", func(e supervisor.Event) bool {
		s, ok := e.(supervisor.StateEvent)
		return ok && s.Info.Restarts == 1 && s.Info.State == supervisor.Running
	})
	if ps := r.Processes(); ps[0].Restarts != 1 {
		t.Errorf("processes after restart = %+v", ps)
	}
	if err := r.Stop("nope"); err == nil {
		t.Error("stopping an unknown project worked")
	}
	if err := r.SetArgs("api", "--verbose"); err != nil {
		t.Error(err)
	}
	if err := r.Resize(100, 30); err != nil {
		t.Error(err)
	}

	// Detaching leaves the session running.
	if err := r.Detach(); err != nil {
		t.Fatal(err)
	}
	if r.Lost() != nil {
		t.Errorf("Lost = %v after detaching", r.Lost())
	}
	for range r.Events() { // closed
	}
	if ps := sup.Processes(); ps[0].State != supervisor.Running {
		t.Errorf("after detaching: %+v", ps)
	}
	if err := r.Note("api", "x"); !errors.Is(err, ErrSessionEnded) {
		t.Errorf("command after detaching: %v", err)
	}
}

func TestAttachTwoViews(t *testing.T) {
	sup, srv := liveSession(t, map[string]string{"api": "exec sleep 30"}, nil)
	a, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	b, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	sup.Note("api", "for both")
	waitFor(t, a, "a's line", lineIs("for both"))
	waitFor(t, b, "b's line", lineIs("for both"))
}

func TestAttachStopUsesQuit(t *testing.T) {
	quits := make(chan bool, 2)
	_, srv := liveSession(t, map[string]string{"api": "exec sleep 30"}, func(force bool) { quits <- force })
	r, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	stopped := r.StopAll()
	if force := <-quits; force {
		t.Error("StopAll forced")
	}
	r.KillAll()
	if force := <-quits; !force {
		t.Error("KillAll didn't force")
	}
	srv.Close() // the session ends
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll's channel stayed open after the session ended")
	}
	if r.Lost() != nil {
		t.Errorf("Lost = %v, but the view stopped the session", r.Lost())
	}
}

func TestAttachWithoutQuitStopsProcesses(t *testing.T) {
	sup, srv := liveSession(t, map[string]string{"api": "exec sleep 30"}, nil)
	r, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	r.StopAll()
	for deadline := time.Now().Add(5 * time.Second); sup.Processes()[0].State != supervisor.Stopped; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("api = %+v", sup.Processes()[0])
		}
	}
}

func TestAttachSessionEnds(t *testing.T) {
	_, srv := liveSession(t, map[string]string{"api": "exec sleep 30"}, nil)
	r, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	timeout := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-r.Events():
		case <-timeout:
			t.Fatal("events not closed when the session ended")
		}
	}
	if !errors.Is(r.Lost(), ErrSessionEnded) {
		t.Errorf("Lost = %v", r.Lost())
	}
	if pids := r.Pids(); pids != nil {
		t.Errorf("pids from an ended session: %v", pids)
	}
}

func TestAttachPendingLines(t *testing.T) {
	_, srv := liveSession(t, map[string]string{"api": "pty:printf 'branch: '; exec sleep 30"}, nil)
	// The pending line comes a moment after the start.
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := Attach(srv.Path())
		if err != nil {
			t.Fatal(err)
		}
		pending := r.Snapshot().Pending["api"]
		r.Close()
		if pending == "branch: " {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending = %q", pending)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAttachNotAttachable(t *testing.T) {
	s := listen(t, shortDir(t), newTarget(), 1)
	if _, err := Attach(s.Path()); err == nil || err.Error() != "this session can't be attached to" {
		t.Errorf("err = %v", err)
	}
}

func TestAttachDrop(t *testing.T) {
	sup, srv := liveSession(t, map[string]string{"api": "exec sleep 30"}, func(bool) { t.Error("dropping a view stopped the session") })
	r, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	reason := errors.New("hung up")
	r.Drop(reason)
	for range r.Events() {
	}
	if r.Lost() != reason {
		t.Errorf("Lost = %v", r.Lost())
	}
	if sup.Processes()[0].State != supervisor.Running {
		t.Errorf("api = %+v", sup.Processes()[0])
	}
}

// waitAttached waits until the session's status counts n views.
func waitAttached(t *testing.T, srv *Server, n int) Status {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		replies, err := collect(t, srv.Path(), Request{Op: OpStatus})
		if err != nil {
			t.Fatal(err)
		}
		st := *replies[0].Status
		if st.Attached == n {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("attached = %d, want %d", st.Attached, n)
		}
	}
}

func TestAttachHostDisconnectRule(t *testing.T) {
	dropped := make(chan struct{}, 4)
	_, srv := liveSessionWith(t, map[string]string{"api": "exec sleep 30"}, Info{
		Host:    true,
		Dropped: func() { dropped <- struct{}{} },
	})
	attach := func() *Remote {
		t.Helper()
		r, err := Attach(srv.Path())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	noDrop := func(what string) {
		t.Helper()
		select {
		case <-dropped:
			t.Errorf("%s stopped the session", what)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if srv.Viewed() {
		t.Error("viewed before any view attached")
	}

	a, b := attach(), attach()
	if st := waitAttached(t, srv, 2); !st.Host {
		t.Error("status doesn't say it's a host")
	}
	if !srv.Viewed() {
		t.Error("not viewed with two views")
	}
	a.Drop(errors.New("closed"))
	waitAttached(t, srv, 1)
	noDrop("dropping one of two views")
	if err := b.Detach(); err != nil {
		t.Fatal(err)
	}
	waitAttached(t, srv, 0)
	noDrop("detaching the last view")

	c := attach()
	waitAttached(t, srv, 1)
	c.Close() // as when its terminal is closed
	select {
	case <-dropped:
	case <-time.After(5 * time.Second):
		t.Fatal("the last view went away without detaching, and the session went on")
	}

	attach()
	waitAttached(t, srv, 1)
	srv.Close() // the session ends: its views go with it
	noDrop("the session ending")
}

func TestAttachSessionEndsWithItsLastEvents(t *testing.T) {
	sup, srv := liveSession(t, map[string]string{"api": "exec sleep 30"}, nil)
	r, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	// As a session ends: the supervisor closes, then the server.
	<-sup.StopAll()
	sup.Close()
	srv.Close()
	waitFor(t, r, "the final state", func(e supervisor.Event) bool {
		s, ok := e.(supervisor.StateEvent)
		return ok && s.Info.State == supervisor.Stopped
	})
}
