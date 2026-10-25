// Package ports finds the TCP ports processes listen on: /proc on Linux,
// lsof on macOS, GetExtendedTcpTable on Windows.
package ports

import (
	"cmp"
	"errors"
	"slices"
)

// ErrUnsupported is returned when listening ports can't be read on this
// system (e.g. lsof is not installed on macOS).
var ErrUnsupported = errors.New("listening ports are not available on this system")

// Listener is a TCP socket in LISTEN state and the process owning it.
type Listener struct {
	Pid  int
	Port int
}

// Listeners returns the TCP sockets in LISTEN state, IPv4 and IPv6, owned by
// any of pids, or by any process that can be inspected when pids is nil. A
// port open on several addresses is listed once per process. The result is
// sorted by pid, then port.
func Listeners(pids []int) ([]Listener, error) {
	var want map[int]bool
	if pids != nil {
		want = make(map[int]bool, len(pids))
		for _, pid := range pids {
			want[pid] = true
		}
	}
	ls, err := listeners(want)
	return tidy(ls), err
}

// Name returns the name of a process, e.g. "node", or "" if it can't be
// found.
func Name(pid int) string {
	return processName(pid)
}

// tidy sorts listeners by pid and port, and drops duplicates.
func tidy(ls []Listener) []Listener {
	slices.SortFunc(ls, func(a, b Listener) int {
		return cmp.Or(cmp.Compare(a.Pid, b.Pid), cmp.Compare(a.Port, b.Port))
	})
	return slices.Compact(ls)
}
