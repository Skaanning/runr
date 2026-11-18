package main

import (
	"os"

	"github.com/Skaanning/runr/internal/control"
	"github.com/Skaanning/runr/internal/tui"
)

// background is how the launcher reaches the sessions running elsewhere:
// over their control sockets in dir.
type background struct{ dir string }

// newBackground returns the launcher's view of the running sessions, or
// nil if they can't be reached.
func newBackground() tui.Background {
	dir := controlDir()
	if dir == "" {
		return nil
	}
	return background{dir}
}

func (b background) List() []tui.RunningSession {
	found, _ := control.Sessions(b.dir) // problems show in runr status
	var out []tui.RunningSession
	for _, s := range found {
		st := s.Status
		if st.Pid == os.Getpid() {
			continue // the launcher's own session (in this process)
		}
		rs := tui.RunningSession{Pid: st.Pid, Titles: st.Titles, Started: st.Started, Host: st.Host, Attached: st.Attached}
		for _, p := range st.Processes {
			rs.States = append(rs.States, p.State)
		}
		out = append(out, rs)
	}
	return out
}

func (b background) Attach(pid int) (tui.Attached, error) {
	path, err := control.SocketPath(b.dir, pid)
	if err != nil {
		return tui.Attached{}, err
	}
	r, err := control.Attach(path)
	if err != nil {
		return tui.Attached{}, err
	}
	snap := r.Snapshot()
	return tui.Attached{Session: r, Config: snap.Config, Titles: snap.Titles}, nil
}

func (b background) Down(pid int) error {
	path, err := control.SocketPath(b.dir, pid)
	if err != nil {
		return err
	}
	return control.Do(path, control.Request{Op: control.OpDown}, func(control.Reply) error { return nil })
}
