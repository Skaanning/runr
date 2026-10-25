// Package sessions remembers the processes each runr session runs, so that
// processes left over when runr itself was killed can be found and stopped
// by the next runr (Unix; on Windows a Job Object takes them down with runr).
package sessions

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrUnsupported is returned where sessions aren't recorded (Windows, and
// Unix systems where process start times can't be read).
var ErrUnsupported = errors.New("session files are not supported on this system")

// State is the content of a session file.
type State struct {
	Pid      int     `json:"pid"`
	Start    string  `json:"start"` // runr's start time, see procStart
	Children []Child `json:"children"`
}

// Child is a live process of a session: the leader of a project's process
// group.
type Child struct {
	Project string `json:"project"`
	Pid     int    `json:"pid"`
	Pgid    int    `json:"pgid"`
	Start   string `json:"start"` // guards against pid reuse
	Command string `json:"command"`
}

// Orphan is the session file of a runr process that is gone, with the
// children that still run.
type Orphan struct {
	Path     string
	Children []Child
}

// Summary describes orphaned processes for a warning, e.g. "2 processes
// left over from a previous runr session: api (pid 4123), db (pid 4120)".
func Summary(orphans []Orphan) string {
	what, which := Describe(orphans)
	return what + ": " + which
}

// Describe returns what Summary says in two parts: how many processes are
// left over from where ("2 processes left over from a previous runr
// session"), and which ones ("api (pid 4123), db (pid 4120)").
func Describe(orphans []Orphan) (what, which string) {
	var names []string
	for _, o := range orphans {
		for _, c := range o.Children {
			names = append(names, c.Project+" (pid "+strconv.Itoa(c.Pid)+")")
		}
	}
	noun := "processes"
	if len(names) == 1 {
		noun = "process"
	}
	from := "a previous runr session"
	if len(orphans) > 1 {
		from = "previous runr sessions"
	}
	return fmt.Sprintf("%d %s left over from %s", len(names), noun, from), strings.Join(names, ", ")
}
