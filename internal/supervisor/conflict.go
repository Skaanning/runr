package supervisor

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/Skaanning/runr/internal/ports"
	"github.com/Skaanning/runr/internal/proc"
)

// conflictTimeout bounds the connection attempt that checks whether a port
// is taken. A variable so tests can change it.
var conflictTimeout = 250 * time.Millisecond

// knownPorts returns the ports a project is known to listen on, sorted:
// ready_port, the port of a ready_url on this machine, and the ports seen
// earlier in the session (RememberPorts).
func knownPorts(m *managed) []int {
	var out []int
	if p := m.project.ReadyPort; p != 0 {
		out = append(out, p)
	}
	if p := localURLPort(m.project.ReadyURL); p != 0 {
		out = append(out, p)
	}
	for p := range m.seenPorts {
		out = append(out, p)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// localURLPort returns the port of an http(s) URL on this machine (explicit
// or the scheme's default), or 0 for other URLs.
func localURLPort(raw string) int {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return 0
	}
	switch host := u.Hostname(); {
	case host == "localhost":
	case net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback():
	default:
		return 0
	}
	if p := u.Port(); p != "" {
		n, _ := strconv.Atoi(p)
		return n
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

// checkPorts warns when another process already listens on one of the
// ports of a run that just started. It works off the loop and never holds
// the run back; the warning is a system line and a WarningEvent.
func (s *Supervisor) checkPorts(m *managed, gen int, p *proc.Process, ps []int) {
	go func() {
		for _, port := range ps {
			ctx, cancel := context.WithTimeout(s.ctx, conflictTimeout)
			taken := dialOK(ctx, net.JoinHostPort("localhost", strconv.Itoa(port)))
			cancel()
			if !taken {
				continue
			}
			text, ok := portOwner(port, p)
			if !ok {
				continue // the run itself got there first
			}
			s.post(func() {
				if m.gen == gen {
					s.warn(m, text)
				}
			})
		}
	}()
}

// portOwner describes who listens on a port, e.g. "port 8080 is in use by
// pid 1234 (node)". It returns false when only the processes of p (the run
// being checked) listen there.
func portOwner(port int, p *proc.Process) (string, bool) {
	ls, err := ports.Listeners(nil)
	if err != nil {
		return fmt.Sprintf("port %d is already in use", port), true
	}
	own := proc.Pids([]*proc.Process{p})[0]
	ownOnly := false
	for _, l := range ls {
		if l.Port != port {
			continue
		}
		if slices.Contains(own, l.Pid) {
			ownOnly = true
			continue
		}
		if name := ports.Name(l.Pid); name != "" {
			return fmt.Sprintf("port %d is in use by pid %d (%s)", port, l.Pid, name), true
		}
		return fmt.Sprintf("port %d is in use by pid %d", port, l.Pid), true
	}
	// Nobody found: another user's process, or one that has gone since.
	return fmt.Sprintf("port %d is already in use", port), !ownOnly
}
