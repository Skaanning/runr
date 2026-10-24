package supervisor

import (
	"regexp"
	"sync/atomic"
	"time"

	"runr/internal/config"
	"runr/internal/proc"
)

// The methods in this file run on the loop goroutine.

// spawn starts the process for m. Failures to start are reported as Crashed.
func (s *Supervisor) spawn(m *managed) {
	if !m.info.StartedAt.IsZero() {
		m.info.Restarts++
	}
	m.gen++
	m.stopRequested = false
	m.restartPending = false
	m.info.StartedAt = time.Now()
	m.info.Exit = proc.ExitStatus{}
	m.info.Pid = 0

	// Logged before spawning so it precedes the process's first output.
	s.system(m, "starting: %s", m.project.Cmd)
	spec, err := s.spec(m.project)
	if err == nil {
		m.proc, err = proc.Start(spec, s.lineHandler(m))
	}
	if err != nil {
		m.proc = nil
		m.info.Exit = proc.ExitStatus{Code: -1, Err: err}
		s.system(m, "failed to start: %v", err)
		s.setState(m, Crashed)
		s.schedule()
		return
	}

	m.info.Pid = m.proc.Pid()
	gen, p := m.gen, m.proc
	s.running.Add(1)
	go func() {
		<-p.Done()
		s.post(func() { s.exited(m, gen, p.Status()) })
		s.running.Done()
	}()

	s.unblockDependents(m)
	if m.readyRe != nil {
		s.setState(m, Starting)
		return
	}
	s.setState(m, Running)
	s.schedule()
}

// lineHandler returns the output callback for the current run of m. It stores
// each line and, while the run is not ready yet, checks it against ready_log.
// It runs on the process's reader goroutine, not the loop.
func (s *Supervisor) lineHandler(m *managed) func(proc.Line) {
	name, gen, re := m.name, m.gen, m.readyRe
	var ready atomic.Bool
	return func(l proc.Line) {
		stream := Stdout
		if l.Stream == proc.Stderr {
			stream = Stderr
		}
		s.events.push(LineEvent{Line: s.logs.Append(name, stream, l.Text)})

		if re != nil && !ready.Load() && re.MatchString(stripANSI(l.Text)) {
			ready.Store(true)
			s.post(func() { s.becameReady(m, gen) })
		}
	}
}

func (s *Supervisor) becameReady(m *managed, gen int) {
	if gen != m.gen || m.info.State != Starting {
		return
	}
	s.system(m, "ready")
	s.setState(m, Running)
	s.schedule()
}

// stop begins a graceful stop of m, or cancels it if it has not started yet.
func (s *Supervisor) stop(m *managed) {
	switch m.info.State {
	case Pending, Waiting, Blocked:
		if m.info.State != Pending {
			s.system(m, "cancelled")
		}
		s.setState(m, Stopped)
		s.schedule() // dependents waiting on m become blocked
		return
	case Starting, Running:
	default:
		return // Stopping, or not alive
	}

	m.stopRequested = true
	s.setState(m, Stopping)
	if err := m.proc.Terminate(); err != nil {
		s.system(m, "terminate failed (%v), killing", err)
		_ = m.proc.Kill()
		return
	}
	gen := m.gen
	m.killTimer = time.AfterFunc(s.timeout, func() {
		s.post(func() {
			if m.gen == gen && m.info.State == Stopping {
				s.system(m, "still running after %v, killing", s.timeout)
				_ = m.proc.Kill()
			}
		})
	})
}

// kill force-kills m if it is alive, or cancels it if it is waiting. Blocked
// projects keep their state: there is nothing to kill, and Blocked explains
// why they never ran.
func (s *Supervisor) kill(m *managed) {
	m.restartPending = false
	if !m.info.State.Alive() {
		if m.info.State == Waiting {
			s.stop(m)
		}
		return
	}
	m.stopRequested = true
	if m.info.State != Stopping {
		s.setState(m, Stopping)
	}
	_ = m.proc.Kill()
}

// exited handles the end of a run.
func (s *Supervisor) exited(m *managed, gen int, status proc.ExitStatus) {
	if gen != m.gen {
		return
	}
	if m.killTimer != nil {
		m.killTimer.Stop()
		m.killTimer = nil
	}
	m.proc = nil
	m.info.Pid = 0
	m.info.Exit = status

	switch {
	case m.stopRequested:
		s.system(m, "stopped (%s)", status)
		s.setState(m, Stopped)
	case status.Success():
		s.system(m, "exited (%s)", status)
		s.setState(m, Exited)
	default:
		s.system(m, "crashed (%s)", status)
		s.setState(m, Crashed)
	}

	if m.restartPending && !s.closing && s.shutdown == nil {
		s.request(m)
		return
	}
	s.schedule()
	s.advanceShutdown()
}

// spec builds the process spec for a project: resolved dir and the layered
// environment (base, runr defaults, env_file, env).
func (s *Supervisor) spec(p *config.Project) (proc.Spec, error) {
	dir, err := p.ResolvedDir()
	if err != nil {
		return proc.Spec{}, err
	}
	envFile, err := p.ResolvedEnvFile()
	if err != nil {
		return proc.Spec{}, err
	}
	var fileVars map[string]string
	if envFile != "" {
		if fileVars, err = proc.LoadEnvFile(envFile); err != nil {
			return proc.Spec{}, err
		}
	}
	return proc.Spec{
		Command: p.Cmd,
		Dir:     dir,
		Env:     proc.BuildEnv(s.baseEnv, proc.DefaultEnv, fileVars, p.Env),
	}, nil
}

// ansiPattern matches CSI sequences (colors, cursor movement) and OSC
// sequences (e.g. hyperlinks, window titles).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func stripANSI(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			return ansiPattern.ReplaceAllString(s, "")
		}
	}
	return s
}
