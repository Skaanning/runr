package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/supervisor"
)

// remoteSession is a fakeSession in another process: it can be detached
// from, and reports why it ended.
type remoteSession struct {
	*fakeSession
	detached  int
	detachErr error
	lost      error
}

func (r *remoteSession) Detach() error { r.detached++; return r.detachErr }
func (r *remoteSession) Lost() error   { return r.lost }

func newRemoteModel(t *testing.T) (*runningModel, *remoteSession) {
	t.Helper()
	sess := &remoteSession{fakeSession: newFakeSession("db", "api")}
	m := newRunningModel(sess, testConfig(), []string{"backend"}, newStyles(terminalTheme))
	m.now = testNow
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	return m, sess
}

func quits(cmd tea.Cmd) bool {
	return cmd != nil && cmd() == tea.Quit()
}

func TestDetach(t *testing.T) {
	m, sess := newRemoteModel(t)
	if help := screen(m)[11]; !strings.HasSuffix(help, "d  detach  q  quit") {
		t.Errorf("help = %q", help)
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12}) // narrow: kept with q
	if help := screen(m)[11]; !strings.HasSuffix(help, "d  detach  q  quit") {
		t.Errorf("narrow help = %q", help)
	}
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	_, cmd := m.Update(key("d"))
	if !quits(cmd) || sess.detached != 1 || !m.detached || sess.stopAll != 0 {
		t.Fatalf("d: quit = %v, detached = %d, stopAll = %d", quits(cmd), sess.detached, sess.stopAll)
	}
}

func TestDetachFails(t *testing.T) {
	m, sess := newRemoteModel(t)
	sess.detachErr = errors.New("gone")
	if _, cmd := m.Update(key("d")); quits(cmd) || m.detached {
		t.Fatal("quit although detaching failed")
	}
	if !strings.Contains(screen(m)[11], "can't detach: gone") {
		t.Errorf("help = %q", screen(m)[11])
	}
}

func TestNoDetachForOwnSession(t *testing.T) {
	m, sess := newTestModel(t, 200, 12)
	if _, cmd := m.Update(key("d")); quits(cmd) || sess.stopAll != 0 {
		t.Error("d did something in the session's own view")
	}
	if strings.Contains(screen(m)[11], "d  detach") {
		t.Errorf("help offers d: %q", screen(m)[11])
	}
}

func TestSessionLost(t *testing.T) {
	m, sess := newRemoteModel(t)
	sess.lost = errors.New("the session ended")
	if _, cmd := m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Stopped)}, closed: true}); !quits(cmd) {
		t.Fatal("the view stayed after the session ended")
	}
	if m.lost == nil {
		t.Error("lost not recorded")
	}

	// Closed events after the view stopped the session aren't a loss.
	m, sess = newRemoteModel(t)
	m.Update(key("q"))
	sess.lost = nil
	if _, cmd := m.Update(eventsMsg{closed: true}); quits(cmd) || m.lost != nil {
		t.Error("stopping counted as losing the session")
	}
}
