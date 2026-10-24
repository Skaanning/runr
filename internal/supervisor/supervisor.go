package supervisor

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"runr/internal/config"
	"runr/internal/proc"
)

// ErrClosed is returned by commands sent after Close.
var ErrClosed = errors.New("supervisor is closed")

// Options configures a Supervisor.
type Options struct {
	// BaseEnv is the environment every process starts from; defaults to os.Environ().
	BaseEnv []string
}

// Supervisor runs projects from a config, tracks their state and collects
// their output.
//
// All process state is owned by a single loop goroutine; the public methods
// send it messages. Output lines bypass the loop and go straight to Logs and
// Events, so heavy output never delays commands.
type Supervisor struct {
	cfg     *config.Config
	logs    *Logs
	baseEnv []string
	timeout time.Duration

	msgs      chan func()
	closed    chan struct{}
	closeOnce sync.Once
	events    *eventQueue
	running   sync.WaitGroup // live processes, for Close

	// Owned by the loop goroutine.
	procs   map[string]*managed
	closing bool // set by Close; no new processes are started
}

type managed struct {
	name    string
	project *config.Project
	info    ProcessInfo

	proc           *proc.Process
	gen            int // incremented on every start; stale messages are ignored
	stopRequested  bool
	restartPending bool
	killTimer      *time.Timer
}

// New creates a supervisor for the projects in cfg. Nothing is started until
// Start is called. The caller must keep reading Events until it is closed.
func New(cfg *config.Config, opts Options) *Supervisor {
	baseEnv := opts.BaseEnv
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	s := &Supervisor{
		cfg:     cfg,
		logs:    NewLogs(cfg.Settings.ScrollbackLines()),
		baseEnv: baseEnv,
		timeout: cfg.Settings.ShutdownTimeout(),
		msgs:    make(chan func(), 64),
		closed:  make(chan struct{}),
		events:  newEventQueue(),
		procs:   map[string]*managed{},
	}
	go s.loop()
	return s
}

// Events delivers state changes and log lines in order. It is closed after Close.
func (s *Supervisor) Events() <-chan Event {
	return s.events.out
}

// Logs returns the store holding every project's recent output.
func (s *Supervisor) Logs() *Logs {
	return s.logs
}

func (s *Supervisor) loop() {
	for {
		select {
		case fn := <-s.msgs:
			fn()
		case <-s.closed:
			return
		}
	}
}

// post runs fn on the loop goroutine without waiting. It must not be called
// from the loop goroutine itself.
func (s *Supervisor) post(fn func()) {
	select {
	case s.msgs <- fn:
	case <-s.closed:
	}
}

// call runs fn on the loop goroutine and returns its result.
func (s *Supervisor) call(fn func() error) error {
	reply := make(chan error, 1)
	select {
	case s.msgs <- func() { reply <- fn() }:
	case <-s.closed:
		return ErrClosed
	}
	select {
	case err := <-reply:
		return err
	case <-s.closed:
		return ErrClosed
	}
}

// Processes returns a snapshot of every project in the session, sorted by name.
func (s *Supervisor) Processes() []ProcessInfo {
	var out []ProcessInfo
	_ = s.call(func() error {
		s.closing = true
		for _, m := range s.procs {
			out = append(out, m.info)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Start starts a project. It fails if the project is unknown or already alive.
func (s *Supervisor) Start(name string) error {
	return s.call(func() error {
		m, err := s.managed(name)
		if err != nil {
			return err
		}
		if m.info.State.Alive() {
			return fmt.Errorf("%s is already %s", name, m.info.State)
		}
		if s.closing {
			return ErrClosed
		}
		s.spawn(m)
		return nil
	})
}

// Stop asks a project to stop: it is terminated gracefully and killed if it
// is still alive after the shutdown timeout. Stop returns immediately; the
// outcome is reported through Events. Stopping a project that is not alive
// is a no-op.
func (s *Supervisor) Stop(name string) error {
	return s.call(func() error {
		m, err := s.managed(name)
		if err != nil {
			return err
		}
		m.restartPending = false
		s.stop(m)
		return nil
	})
}

// Restart stops a project (if alive) and starts it again once it has exited.
func (s *Supervisor) Restart(name string) error {
	return s.call(func() error {
		m, err := s.managed(name)
		if err != nil {
			return err
		}
		if s.closing {
			return ErrClosed
		}
		if !m.info.State.Alive() {
			s.spawn(m)
			return nil
		}
		m.restartPending = true
		s.stop(m)
		return nil
	})
}

// Kill force-kills a project immediately.
func (s *Supervisor) Kill(name string) error {
	return s.call(func() error {
		m, err := s.managed(name)
		if err != nil {
			return err
		}
		if m.info.State.Alive() {
			m.stopRequested = true
			m.restartPending = false
			s.setState(m, Stopping)
			_ = m.proc.Kill()
		}
		return nil
	})
}

// Close force-kills every live process, waits for them to exit, and shuts
// the supervisor down. Events is closed once all remaining events are delivered.
func (s *Supervisor) Close() {
	_ = s.call(func() error {
		s.closing = true
		for _, m := range s.procs {
			if m.info.State.Alive() {
				m.stopRequested = true
				m.restartPending = false
				_ = m.proc.Kill()
			}
		}
		return nil
	})
	s.running.Wait()
	s.closeOnce.Do(func() {
		close(s.closed)
		s.events.close()
	})
}

// managed returns the session entry for a project, adding it if needed.
// Must run on the loop goroutine.
func (s *Supervisor) managed(name string) (*managed, error) {
	if m := s.procs[name]; m != nil {
		return m, nil
	}
	p := s.cfg.Projects[name]
	if p == nil {
		return nil, fmt.Errorf("unknown project %q", name)
	}
	m := &managed{name: name, project: p, info: ProcessInfo{Name: name, State: Pending}}
	s.procs[name] = m
	return m, nil
}

// spawn starts the process for m. Failures to start are reported as Crashed.
// Must run on the loop goroutine.
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
		name := m.name
		m.proc, err = proc.Start(spec, func(l proc.Line) {
			stream := Stdout
			if l.Stream == proc.Stderr {
				stream = Stderr
			}
			s.events.push(LineEvent{Line: s.logs.Append(name, stream, l.Text)})
		})
	}
	if err != nil {
		m.proc = nil
		m.info.Exit = proc.ExitStatus{Code: -1, Err: err}
		s.system(m, "failed to start: %v", err)
		s.setState(m, Crashed)
		return
	}

	m.info.Pid = m.proc.Pid()
	s.setState(m, Running)

	gen, p := m.gen, m.proc
	s.running.Add(1)
	go func() {
		<-p.Done()
		s.post(func() { s.exited(m, gen, p.Status()) })
		s.running.Done()
	}()
}

// stop begins a graceful stop of m. Must run on the loop goroutine.
func (s *Supervisor) stop(m *managed) {
	if !m.info.State.Alive() {
		return
	}
	m.stopRequested = true
	if m.info.State == Stopping {
		return // already on its way down
	}
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

// exited handles the end of a run. Must run on the loop goroutine.
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

	if m.restartPending && !s.closing {
		s.spawn(m)
	}
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

func (s *Supervisor) setState(m *managed, state State) {
	m.info.State = state
	s.events.push(StateEvent{Info: m.info})
}

// system appends a runr-generated line to a project's log.
func (s *Supervisor) system(m *managed, format string, args ...any) {
	line := s.logs.Append(m.name, System, fmt.Sprintf(format, args...))
	s.events.push(LineEvent{Line: line})
}
