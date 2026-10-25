package tui

import tea "charm.land/bubbletea/v2"

// Leftovers are processes an earlier runr session left running because
// runr itself was killed. The launcher and the running view ask about them
// before anything else.
type Leftovers struct {
	What   string        // e.g. "2 processes left over from a previous runr session"
	Which  string        // e.g. "api (pid 4123), db (pid 4120)"
	Kill   func() string // stops them (may take the shutdown timeout); returns a report
	Forget func()        // leaves them running and forgets them
}

// leftoversMsg reports that the question about leftovers was answered and
// dealt with.
type leftoversMsg struct {
	owner  any
	report string // for the footer; empty when they were left running
}

// leftoversQuestion asks whether to kill the leftovers. Both answers work
// in the background and end with a leftoversMsg for owner; notify shows
// that the kill is under way.
func leftoversQuestion(l *Leftovers, owner any, notify func(string)) *question {
	return &question{
		// The question first: a narrow screen cuts the list instead.
		prompt: "Kill " + l.What + "? " + l.Which,
		action: "kill",
		cancel: "leave them",
		yes: func() tea.Cmd {
			notify("stopping the leftover processes…")
			return func() tea.Msg { return leftoversMsg{owner: owner, report: l.Kill()} }
		},
		no: func() tea.Cmd {
			return func() tea.Msg { l.Forget(); return leftoversMsg{owner: owner} }
		},
	}
}

// askLeftovers opens the question about leftover processes in the launcher;
// starting waits until they are dealt with.
func (m *launcherModel) askLeftovers(l *Leftovers) {
	m.ask = leftoversQuestion(l, m, func(s string) { m.notice, m.cleaning = s, true })
}

// askLeftovers opens the question about leftover processes in the running
// view, whose session starts once they are dealt with (see leftoversMsg).
func (m *runningModel) askLeftovers(l *Leftovers) {
	m.confirm = leftoversQuestion(l, m, func(s string) { m.notice = s })
}
