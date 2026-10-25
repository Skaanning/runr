package tui

import (
	"errors"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/ports"
)

const portInterval = 2 * time.Second // between refreshes while processes run

type (
	portsMsg struct { // a refresh finished
		owner *portTracker
		ports map[string][]int
		err   error
	}
	portTickMsg struct { // time for the next refresh
		owner *portTracker
		gen   int
	}
)

// portTracker keeps the listening ports of the session's processes up to
// date. Like gitTracker, refreshes run in the background, one at a time; a
// refresh requested while one runs follows it. The next one is scheduled
// portInterval after the last finished, but only while processes run.
type portTracker struct {
	pids   func() map[string][]int                    // process trees by project (Session.Pids)
	lookup func(pids []int) ([]ports.Listener, error) // ports.Listeners
	state  map[string][]int                           // listening ports by project, sorted
	busy   bool                                       // a refresh is running
	again  bool                                       // refresh again once it finishes
	gen    int                                        // generation of the scheduled tick
	off    bool                                       // ports can't be read on this system
}

func newPortTracker(pids func() map[string][]int) *portTracker {
	return &portTracker{pids: pids, lookup: ports.Listeners, state: map[string][]int{}}
}

// get returns the ports a project listens on.
func (t *portTracker) get(name string) []int {
	return t.state[name]
}

// refresh reads the listening ports in the background.
func (t *portTracker) refresh() tea.Cmd {
	if t.off {
		return nil
	}
	if t.busy {
		t.again = true
		return nil
	}
	t.busy = true
	pids, lookup := t.pids, t.lookup
	return func() tea.Msg {
		owner := map[int]string{} // pid → project
		var all []int
		for name, tree := range pids() {
			for _, pid := range tree {
				owner[pid] = name
				all = append(all, pid)
			}
		}
		found := map[string][]int{}
		if len(all) == 0 {
			return portsMsg{owner: t, ports: found} // nil would mean every process
		}
		ls, err := lookup(all)
		for _, l := range ls {
			name := owner[l.Pid]
			found[name] = append(found[name], l.Port)
		}
		for name, ps := range found {
			slices.Sort(ps)
			found[name] = slices.Compact(ps)
		}
		return portsMsg{owner: t, ports: found, err: err}
	}
}

// update handles the tracker's messages and returns what to do next: the
// next refresh, or the tick that triggers it while live (processes run).
// Messages of other trackers are ignored.
func (t *portTracker) update(msg tea.Msg, live bool) tea.Cmd {
	switch msg := msg.(type) {
	case portsMsg:
		if msg.owner != t {
			return nil
		}
		t.state, t.busy = msg.ports, false
		if errors.Is(msg.err, ports.ErrUnsupported) {
			t.off, t.again = true, false
			return nil
		}
		if t.again {
			t.again = false
			return t.refresh()
		}
		t.gen++
		if !live {
			return nil // a process starting refreshes again
		}
		gen := t.gen
		return tea.Tick(portInterval, func(time.Time) tea.Msg { return portTickMsg{owner: t, gen: gen} })
	case portTickMsg:
		if msg.owner == t && msg.gen == t.gen {
			return t.refresh()
		}
	}
	return nil
}

// portsLabel shows the first port and how many more there are, e.g.
// ":5173 +1", or "" for none.
func portsLabel(ps []int) string {
	if len(ps) == 0 {
		return ""
	}
	label := ":" + strconv.Itoa(ps[0])
	if len(ps) > 1 {
		label += " +" + strconv.Itoa(len(ps)-1)
	}
	return label
}
