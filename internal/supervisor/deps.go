package supervisor

// The methods in this file run on the loop goroutine.

type depStatus int

const (
	depsReady   depStatus = iota // every dependency is ready
	depsPending                  // some dependency may still become ready
	depsFailed                   // some dependency ended without becoming ready
)

// depsOf reports whether m can start now, and if not, names the dependency
// it is waiting on or that failed.
func (s *Supervisor) depsOf(m *managed) (depStatus, string) {
	status, culprit := depsReady, ""
	for _, dep := range m.project.DependsOn {
		dm := s.procs[dep]
		if dm == nil {
			return depsFailed, dep
		}
		switch dm.info.State {
		case Running, Exited:
			// Ready. Exited (code 0) counts so one-shot jobs such as
			// migrations work as dependencies.
		case Crashed, Stopped, Blocked:
			if !dm.restartPending {
				return depsFailed, dep
			}
			status, culprit = depsPending, dep
		default:
			status, culprit = depsPending, dep
		}
	}
	return status, culprit
}

// request starts m once its dependencies are ready: immediately if they
// already are, otherwise it becomes Waiting.
func (s *Supervisor) request(m *managed) {
	switch status, dep := s.depsOf(m); status {
	case depsReady:
		s.spawn(m)
	default:
		// A failed dependency is reported by schedule, which blocks m.
		s.system(m, "waiting for %s", dep)
		s.setState(m, Waiting)
		s.unblockDependents(m)
		s.schedule()
	}
}

// schedule starts every Waiting project whose dependencies are ready and
// blocks those whose dependencies failed, repeating until nothing changes.
func (s *Supervisor) schedule() {
	if s.closing || s.shutdown != nil {
		return
	}
	for changed := true; changed; {
		changed = false
		for _, name := range s.order {
			m := s.procs[name]
			if m.info.State != Waiting {
				continue
			}
			switch status, dep := s.depsOf(m); status {
			case depsReady:
				s.spawn(m)
				changed = true
			case depsFailed:
				s.systemError(m, "blocked: dependency %s is %s", dep, s.procs[dep].info.State)
				s.setState(m, Blocked)
				changed = true
			}
		}
	}
}

// unblockDependents moves projects that were blocked because of m back to
// Waiting, transitively, now that m is starting again. schedule re-blocks any
// that still have another failed dependency.
func (s *Supervisor) unblockDependents(m *managed) {
	for _, name := range s.order {
		d := s.procs[name]
		if d.info.State != Blocked || !dependsOn(d, m.name) {
			continue
		}
		s.system(d, "waiting for %s", m.name)
		s.setState(d, Waiting)
		s.unblockDependents(d)
	}
}

func dependsOn(m *managed, dep string) bool {
	for _, d := range m.project.DependsOn {
		if d == dep {
			return true
		}
	}
	return false
}

// shutdown tracks a StopAll in progress.
type shutdown struct {
	levels  [][]string // remaining dependency levels, dependents first
	waiters []chan struct{}
}

func (s *Supervisor) beginShutdown(done chan struct{}) {
	if s.shutdown != nil {
		s.shutdown.waiters = append(s.shutdown.waiters, done)
		return
	}

	var alive []string
	for _, name := range s.order {
		m := s.procs[name]
		m.restartPending = false
		switch {
		case m.info.State.Alive():
			alive = append(alive, name)
		case m.info.State == Waiting || m.info.State == Pending:
			s.system(m, "cancelled")
			s.setState(m, Stopped)
		}
	}

	levels, err := s.cfg.Levels(alive)
	if err != nil {
		// Only possible with an invalid config; stop everything at once.
		levels = [][]string{alive}
	}
	// Reverse: the deepest dependents stop first.
	for i, j := 0, len(levels)-1; i < j; i, j = i+1, j-1 {
		levels[i], levels[j] = levels[j], levels[i]
	}
	s.shutdown = &shutdown{levels: levels, waiters: []chan struct{}{done}}
	s.advanceShutdown()
}

// advanceShutdown stops the next dependency level once the current one has
// fully exited, and finishes the shutdown when every level is done.
func (s *Supervisor) advanceShutdown() {
	sd := s.shutdown
	if sd == nil {
		return
	}
	for len(sd.levels) > 0 {
		var stillAlive []string
		for _, name := range sd.levels[0] {
			m := s.procs[name]
			if m.info.State.Alive() {
				stillAlive = append(stillAlive, name)
				s.stop(m) // no-op if already stopping
			}
		}
		if len(stillAlive) > 0 {
			return
		}
		sd.levels = sd.levels[1:]
	}
	for _, w := range sd.waiters {
		close(w)
	}
	s.shutdown = nil
}
