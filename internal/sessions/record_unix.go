//go:build !windows

package sessions

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Skaanning/runr/internal/state"
)

// Dir is where session files live: sessions in runr's state directory
// (see state.Dir).
func Dir() (string, error) {
	base, err := state.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "sessions"), nil
}

// Recorder keeps the session file of this runr process up to date. Errors
// writing it are ignored: the file only helps a later cleanup. A nil
// Recorder records nothing.
type Recorder struct {
	path  string
	mu    sync.Mutex
	state State
}

// Open starts recording this process's session in dir.
func Open(dir string) (*Recorder, error) {
	start, err := procStart(os.Getpid())
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	pid := os.Getpid()
	// The start time in the name keeps the file of an earlier runr that had
	// the same pid.
	name := strconv.Itoa(pid) + "-" + sanitize(start) + ".json"
	r := &Recorder{path: filepath.Join(dir, name), state: State{Pid: pid, Start: start, Children: []Child{}}}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.write()
	return r, nil
}

// Spawned records a project's new process, the leader of its own process
// group, started in dir; stopCmd is the project's stop_cmd.
func (r *Recorder) Spawned(project string, pid int, command, dir, stopCmd string) {
	if r == nil {
		return
	}
	start, err := procStart(pid)
	if err != nil {
		return // already gone
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Children = slices.DeleteFunc(r.state.Children, func(c Child) bool { return c.Project == project })
	r.state.Children = append(r.state.Children, Child{Project: project, Pid: pid, Pgid: pid, Start: start, Command: command, Dir: dir, StopCmd: stopCmd})
	r.write()
}

// Exited records that a project's process group has ended.
func (r *Recorder) Exited(project string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Children = slices.DeleteFunc(r.state.Children, func(c Child) bool { return c.Project == project })
	r.write()
}

// Close deletes the session file, after a clean shutdown.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	os.Remove(r.path)
}

// write replaces the file atomically.
func (r *Recorder) write() {
	b, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".tmp-*")
	if err != nil {
		return
	}
	_, err = tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), r.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
}

// Find returns the session files of runr processes that are gone and still
// have live children, or children with a stop_cmd to run: those whose
// process is gone too are marked Gone (their output pipes died with runr,
// so they may not have cleaned up, e.g. docker compose its containers).
// Files of gone sessions without such children, and unreadable ones, are
// deleted; those of running runr processes are left alone.
func Find(dir string) ([]Orphan, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Orphan
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s State
		if json.Unmarshal(b, &s) != nil {
			os.Remove(path)
			continue
		}
		if alive(s.Pid, s.Start) {
			continue // another runr session
		}
		var live []Child
		for _, c := range s.Children {
			if c.Gone = !childAlive(c); !c.Gone || c.StopCmd != "" {
				live = append(live, c)
			}
		}
		if len(live) == 0 {
			os.Remove(path)
			continue
		}
		out = append(out, Orphan{Path: path, Children: live})
	}
	return out, nil
}

// alive reports whether the process pid runs and started at start.
func alive(pid int, start string) bool {
	got, err := procStart(pid)
	return err == nil && got == start
}

// childAlive reports whether a child's process group still runs. While its
// leader runs, the start time tells whether it is the same process; once
// the leader is gone, its pid can't be reused while the group has members.
func childAlive(c Child) bool {
	if _, err := procStart(c.Pid); err == nil {
		return alive(c.Pid, c.Start)
	}
	return groupExists(c.Pgid)
}

func groupExists(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// pollEvery is how often Kill checks whether the groups are gone.
const pollEvery = 20 * time.Millisecond

// Kill stops the orphans like a shutdown: SIGTERM to every process group,
// then SIGKILL to those still running after timeout. It deletes their
// files, and returns the children that had to be killed. Running their
// stop_cmds is up to the caller.
func Kill(orphans []Orphan, timeout time.Duration) (forced []Child) {
	var children []Child
	for _, o := range orphans {
		for _, c := range o.Children {
			if !c.Gone && childAlive(c) {
				syscall.Kill(-c.Pgid, syscall.SIGTERM)
				children = append(children, c)
			}
		}
	}
	running := func() []Child {
		return slices.DeleteFunc(slices.Clone(children), func(c Child) bool { return !groupExists(c.Pgid) })
	}
	for deadline := time.Now().Add(timeout); len(running()) > 0 && time.Now().Before(deadline); {
		time.Sleep(pollEvery)
	}
	forced = running()
	for _, c := range forced {
		syscall.Kill(-c.Pgid, syscall.SIGKILL)
	}
	for deadline := time.Now().Add(time.Second); len(running()) > 0 && time.Now().Before(deadline); {
		time.Sleep(pollEvery)
	}
	Forget(orphans)
	return forced
}

// Forget deletes the orphans' files, leaving their processes running.
func Forget(orphans []Orphan) {
	for _, o := range orphans {
		os.Remove(o.Path)
	}
}

// sanitize keeps a start time usable in a file name.
func sanitize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			b[i] = '_'
		}
	}
	return string(b)
}
