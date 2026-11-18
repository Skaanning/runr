package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/supervisor"
)

// fakeBackground is a Background with sessions set by the test.
type fakeBackground struct {
	list      []RunningSession
	lists     int // calls of List
	attach    map[int]Attached
	attachErr error
	downs     []int
}

func (f *fakeBackground) List() []RunningSession { f.lists++; return slices.Clone(f.list) }
func (f *fakeBackground) Attach(pid int) (Attached, error) {
	if f.attachErr != nil {
		return Attached{}, f.attachErr
	}
	return f.attach[pid], nil
}
func (f *fakeBackground) Down(pid int) error { f.downs = append(f.downs, pid); return nil }

var sessStarted = time.Date(2026, 10, 25, 10, 42, 0, 0, time.Local)

func twoSessions() []RunningSession {
	return []RunningSession{
		{Pid: 4123, Titles: []string{"backend", "web"}, Started: sessStarted, Host: true,
			States: []string{"running", "running", "crashed", "running"}},
		{Pid: 4200, Titles: []string{"db"}, Started: sessStarted.AddDate(0, 0, -1), Attached: 1, States: []string{"starting"}},
	}
}

// newBackgroundLauncher returns a launcher listing bg's sessions.
func newBackgroundLauncher(t *testing.T, bg *fakeBackground) *launcherModel {
	t.Helper()
	m := newLauncherModel(launcherConfig(t), nil, newStyles(terminalTheme))
	m.bg = newBackgroundTracker(bg)
	m.now = func() time.Time { return time.Date(2026, 10, 25, 14, 32, 10, 0, time.Local) }
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	refreshBackground(t, m)
	return m
}

// refreshBackground runs a refresh of the launcher's session list.
func refreshBackground(t *testing.T, m *launcherModel) tea.Cmd {
	t.Helper()
	cmd := m.bg.refresh()
	if cmd == nil {
		t.Fatal("no refresh")
	}
	_, next := m.Update(cmd())
	return next
}

func TestLauncherRunningSection(t *testing.T) {
	m := newBackgroundLauncher(t, &fakeBackground{list: twoSessions()})
	rows := launcherScreen(m)
	want := []string{
		"   Running",
		"   ● session 4123  backend, web · 3 running · 1 crashed · since 10:42 · detached",
		"   ● session 4200  db · 1 starting · since Oct 24 10:42 · 1 view",
		"",
		"   Groups",
	}
	if got := rows[2:7]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Hidden once none runs.
	m.bg.bg.(*fakeBackground).list = nil
	refreshBackground(t, m)
	if rows := launcherScreen(m); slices.Contains(rows, "   Running") || rows[2] != "   Groups" {
		t.Errorf("rows without sessions:\n%s", strings.Join(rows, "\n"))
	}
}

func TestLauncherSessionCursor(t *testing.T) {
	bg := &fakeBackground{list: twoSessions()}
	m := newBackgroundLauncher(t, bg)
	if m.sessCursor != -1 {
		t.Fatal("the cursor starts on a session")
	}
	m.Update(key("up")) // from the first card
	if s, ok := m.currentSession(); !ok || s.Pid != 4200 {
		t.Fatalf("up from the cards: %+v %v", s, ok)
	}
	if _, ok := m.current(); ok {
		t.Error("an item is current on a session")
	}
	if rows := launcherScreen(m); rows[4] != " ▸ ● session 4200  db · 1 starting · since Oct 24 10:42 · 1 view" {
		t.Errorf("cursor row = %q", rows[4])
	}
	if help := launcherScreen(m)[15]; !strings.Contains(help, "enter  attach  x  stop") {
		t.Errorf("help = %q", help)
	}
	if !strings.Contains(launcherScreen(m)[14], "session 4200") {
		t.Errorf("status bar = %q", launcherScreen(m)[14])
	}
	m.Update(key("up"))
	m.Update(key("up")) // stays on the first
	if m.sessCursor != 0 {
		t.Errorf("sessCursor = %d", m.sessCursor)
	}
	// space and enter don't select or start items meanwhile.
	m.Update(key("space"))
	if len(m.selected) > 0 {
		t.Error("space selected something on a session")
	}

	// The cursor stays on its session when the list changes.
	bg.list = []RunningSession{bg.list[1], bg.list[0]}
	refreshBackground(t, m)
	if s, _ := m.currentSession(); s.Pid != 4123 || m.sessCursor != 1 {
		t.Errorf("after reordering: %+v at %d", s, m.sessCursor)
	}
	bg.list = bg.list[:1]
	refreshBackground(t, m)
	if s, _ := m.currentSession(); s.Pid != 4200 {
		t.Errorf("after its session ended: %+v", s)
	}
	bg.list = nil
	refreshBackground(t, m)
	if m.sessCursor != -1 || m.cursor != 0 {
		t.Errorf("after all ended: sessCursor %d, cursor %d", m.sessCursor, m.cursor)
	}

	bg.list = twoSessions()
	refreshBackground(t, m)
	m.Update(key("end"))
	m.Update(key("home"))
	if m.sessCursor != 0 {
		t.Errorf("home: sessCursor = %d", m.sessCursor)
	}
	m.Update(key("down"))
	m.Update(key("down")) // into the cards
	if m.sessCursor != -1 || m.cursor != 0 {
		t.Errorf("down from the last session: sessCursor %d, cursor %d", m.sessCursor, m.cursor)
	}
}

func TestLauncherAttachAndStopSession(t *testing.T) {
	bg := &fakeBackground{list: twoSessions()}
	m := newBackgroundLauncher(t, bg)
	m.Update(key("home"))
	_, cmd := m.Update(key("enter"))
	if cmd == nil || cmd() != (attachMsg{pid: 4123}) {
		t.Fatal("enter doesn't attach")
	}

	m.Update(key("x"))
	if m.ask == nil || !strings.Contains(launcherScreen(m)[15], "Stop session 4123 (backend, web)?") {
		t.Fatalf("no question: %q", launcherScreen(m)[15])
	}
	_, cmd = m.Update(key("enter"))
	if !strings.Contains(launcherScreen(m)[3], "stopping…") {
		t.Errorf("row = %q", launcherScreen(m)[3])
	}
	if _, again := m.Update(key("enter")); again != nil {
		t.Error("attached to a session being stopped")
	}
	_, refresh := m.Update(cmd())
	if !slices.Equal(bg.downs, []int{4123}) || m.notice != "stopped session 4123" || refresh == nil {
		t.Errorf("downs %v, notice %q", bg.downs, m.notice)
	}

	// Jump mode: digits attach.
	m.Update(key("/"))
	if !strings.HasPrefix(launcherScreen(m)[3], " 1 ● session 4123") {
		t.Errorf("jump row = %q", launcherScreen(m)[3])
	}
	_, cmd = m.Update(key("2"))
	if cmd == nil || cmd() != (attachMsg{pid: 4200}) || m.jump {
		t.Error("2 in jump mode doesn't attach to the second session")
	}
}

func TestBackgroundTrackerPauses(t *testing.T) {
	bg := &fakeBackground{list: twoSessions()}
	tr := newBackgroundTracker(bg)
	tr.update(tr.refresh()())
	tr.pause()
	if cmd, ok := tr.update(backgroundTickMsg{owner: tr, gen: tr.gen}); !ok || cmd != nil {
		t.Error("refreshed while paused")
	}
	if cmd := tr.resume(); cmd == nil {
		t.Fatal("resume doesn't refresh")
	} else {
		tr.update(cmd())
	}
	if bg.lists != 2 {
		t.Errorf("List called %d times", bg.lists)
	}
	if cmd, _ := tr.update(backgroundTickMsg{owner: tr, gen: 1}); cmd != nil {
		t.Error("an old tick refreshed")
	}
}

func newBackgroundApp(t *testing.T, bg *fakeBackground) *appModel {
	t.Helper()
	cfg := launcherConfig(t)
	l := newBackgroundLauncher(t, bg)
	app := &appModel{cfg: cfg, bg: bg, launcher: l, start: func([]string, map[string]string) (Session, error) {
		return &remoteSession{fakeSession: newFakeSession("db")}, nil
	}}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	return app
}

func TestAppAttachDetach(t *testing.T) {
	sess := &remoteSession{fakeSession: newFakeSession("db", "api")}
	bg := &fakeBackground{list: twoSessions(), attach: map[int]Attached{4123: {Session: sess, Config: testConfig(), Titles: []string{"backend"}}}}
	app := newBackgroundApp(t, bg)

	bg.attachErr = errors.New("gone")
	app.Update(attachMsg{pid: 4123})
	if app.running != nil || app.launcher.notice != "can't attach: gone" {
		t.Fatalf("running = %v, notice = %q", app.running != nil, app.launcher.notice)
	}
	bg.attachErr = nil
	app.Update(attachMsg{pid: 4123})
	if app.running == nil || !strings.HasPrefix(statusRow(app.running), "  FOLLOW  backend ") {
		t.Fatal("no running view of the session")
	}
	lists := bg.lists
	app.Update(backgroundTickMsg{owner: app.launcher.bg, gen: app.launcher.bg.gen})
	if bg.lists != lists {
		t.Error("the list was refreshed behind the running view")
	}

	app.Update(key("d"))
	if app.running != nil || sess.detached != 1 {
		t.Fatalf("d: running = %v, detached = %d", app.running != nil, sess.detached)
	}
	if !strings.Contains(app.launcher.notice, "detached") {
		t.Errorf("notice = %q", app.launcher.notice)
	}
	if !slices.Contains(launcherScreen(app.launcher), "   Running") {
		t.Error("the launcher doesn't list the sessions")
	}

	// A session that ends elsewhere goes back to the launcher too.
	app.Update(attachMsg{pid: 4123})
	sess.lost = errors.New("the session ended")
	app.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Stopped)}, closed: true})
	if app.running != nil || app.launcher.notice != "the session ended" {
		t.Errorf("after the session ended: running = %v, notice = %q", app.running != nil, app.launcher.notice)
	}

	// q stops it and quits runr, as before.
	sess.lost = nil
	app.Update(attachMsg{pid: 4123})
	app.Update(key("q"))
	if sess.stopAll != 1 || app.running == nil {
		t.Errorf("q: stopAll = %d", sess.stopAll)
	}
}

func TestAppDetachFromStartedSession(t *testing.T) {
	app := newBackgroundApp(t, &fakeBackground{})
	app.Update(startMsg{names: []string{"db"}})
	if app.running == nil {
		t.Fatal("not started")
	}
	app.Update(key("d"))
	if app.running != nil {
		t.Error("d didn't return to the launcher")
	}
}
