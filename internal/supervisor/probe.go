package supervisor

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Skaanning/runr/internal/config"
)

// How often a readiness probe is tried, and how long one attempt may take.
// Variables so tests can speed them up.
var (
	probeEvery   = 250 * time.Millisecond
	probeTimeout = time.Second
)

// probe is a readiness check that is polled from spawn until it succeeds:
// ready_port or ready_url.
type probe struct {
	desc  string // what it waits for, for the log: "port 5432"
	check func(ctx context.Context) bool
}

// newProbe returns the project's polled readiness check, or nil if it has
// none (no condition, ready_log or ready = "exit").
func newProbe(p *config.Project) *probe {
	switch {
	case p.ReadyPort != 0:
		addr := net.JoinHostPort("localhost", strconv.Itoa(p.ReadyPort))
		return &probe{desc: "port " + strconv.Itoa(p.ReadyPort), check: func(ctx context.Context) bool {
			return dialOK(ctx, addr)
		}}
	case p.ReadyURL != "":
		url := p.ReadyURL
		return &probe{desc: url, check: func(ctx context.Context) bool {
			return getOK(ctx, url)
		}}
	}
	return nil
}

// dialOK reports whether a TCP connection to addr succeeds.
func dialOK(ctx context.Context, addr string) bool {
	d := net.Dialer{Timeout: probeTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// probeClient doesn't follow redirects (a 3xx already means the server is
// up) or keep connections around between attempts.
var probeClient = &http.Client{
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// getOK reports whether a GET of url answers with a 2xx or 3xx status.
func getOK(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// startProbe polls m's probe for the current run until it succeeds, the run
// ends or the supervisor closes. It runs on the loop goroutine; the polling
// doesn't.
func (s *Supervisor) startProbe(m *managed) {
	ctx, cancel := context.WithCancel(s.ctx)
	m.cancelProbe = cancel
	gen, pr := m.gen, m.probe
	s.system(m, "waiting for %s", pr.desc)
	go func() {
		defer cancel()
		t := time.NewTicker(probeEvery)
		defer t.Stop()
		for {
			if pr.check(ctx) {
				s.post(func() { s.becameReady(m, gen) })
				return
			}
			select {
			case <-t.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// stopProbe stops m's probe, if one is running.
func (m *managed) stopProbe() {
	if m.cancelProbe != nil {
		m.cancelProbe()
		m.cancelProbe = nil
	}
}
