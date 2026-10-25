package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/supervisor"
)

const (
	toastFor       = 5 * time.Second // how long a toast is shown
	maxToasts      = 3               // stacked at most; a new one replaces the oldest
	toastMinRows   = 8               // log rows needed to show toasts at all
	toastRows      = 4               // a toast's height, with its borders
	toastMinInner  = 32              // the width inside a toast's borders, at least
	toastMinMargin = 16              // log columns left of a toast, at least
)

// toast tells about a process that crashed or was blocked, or warns about
// it, in the corner of the log pane, until it expires or the next key.
type toast struct {
	name  string // the project
	state supervisor.State
	warn  bool   // a warning (e.g. a port is taken), not a state change
	what  string // e.g. "crashed · exit code 1"
	until time.Time
}

// toastMsg prunes expired toasts.
type toastMsg struct{}

// newToast returns the toast for a process that changed state from prev, if
// it crashed or was blocked.
func (m *runningModel) newToast(prev supervisor.State, info supervisor.ProcessInfo) (toast, bool) {
	if info.State == prev || m.stopping {
		return toast{}, false
	}
	t := toast{name: info.Name, state: info.State, until: m.now().Add(toastFor)}
	switch info.State {
	case supervisor.Crashed:
		t.what = "crashed · " + info.Exit.String()
		if info.Exit.Err != nil {
			t.what = "failed to start"
		}
	case supervisor.Blocked:
		t.what = "blocked · " + m.blockedBy(info.Name)
	default:
		return toast{}, false
	}
	return t, true
}

// blockedBy describes the dependency that blocked a project.
func (m *runningModel) blockedBy(name string) string {
	if p := m.cfg.Projects[name]; p != nil {
		for _, dep := range p.DependsOn {
			if e := m.byName[dep]; e != nil {
				switch s := e.info.State; s {
				case supervisor.Crashed, supervisor.Blocked, supervisor.Stopped, supervisor.Exited:
					return dep + " " + s.String()
				}
			}
		}
	}
	return "a dependency failed"
}

// addToast shows t, replacing an older toast of the same project and kind
// (a crash right after a warning, e.g. that its port is taken, keeps the
// warning), or the oldest one when there are too many.
func (m *runningModel) addToast(t toast) {
	m.toasts = slices.DeleteFunc(m.toasts, func(o toast) bool { return o.name == t.name && o.warn == t.warn })
	m.toasts = append(m.toasts, t)
	if len(m.toasts) > maxToasts {
		m.toasts = m.toasts[len(m.toasts)-maxToasts:]
	}
}

// pruneToasts removes the toasts that expired.
func (m *runningModel) pruneToasts() {
	now := m.now()
	m.toasts = slices.DeleteFunc(m.toasts, func(t toast) bool { return !now.Before(t.until) })
}

// toastHint is the second line of a toast: the keys to deal with it.
func (m *runningModel) toastHint(t toast) string {
	key := func(k, desc string) string { return m.st.bold.Render(k) + " " + m.st.faint.Render(desc) }
	e := m.selectedEntry()
	switch {
	case e == nil || e.info.Name != t.name:
		return key("!", "select it")
	case t.warn && m.single != nil && m.singleFor == t.name:
		return m.st.faint.Render("any key to dismiss")
	case t.warn:
		return key("tab", "see its log")
	case m.single != nil && m.singleFor == t.name:
		return key("r", "restart")
	default:
		return key("r", "restart") + "  " + key("tab", "see its log")
	}
}

// drawToasts draws the toasts over the bottom right of the log rows, newest
// at the bottom, as many as fit. It returns rows unchanged when the log
// area is too small.
func (m *runningModel) drawToasts(rows []string, width int) []string {
	n := min(len(m.toasts), len(rows)/toastRows)
	if n == 0 || len(rows) < toastMinRows {
		return rows
	}
	type content struct {
		title, hint string
		warn        bool
	}
	lines := make([]content, n)
	inner := toastMinInner
	for i, t := range m.toasts[len(m.toasts)-n:] {
		label := t.name
		if e := m.byName[t.name]; e != nil {
			label = e.label
		}
		icon := m.st.stateIcon(t.state, "")
		if t.warn {
			icon = m.st.warn.Render("!")
		}
		lines[i] = content{
			title: " " + icon + " " + m.st.bold.Render(label) + m.st.faint.Render(" "+t.what),
			hint:  "   " + m.toastHint(t),
			warn:  t.warn,
		}
		inner = max(inner, ansi.StringWidth(lines[i].title)+1, ansi.StringWidth(lines[i].hint)+1)
	}
	inner = min(inner, width-toastMinMargin-4) // a space and a border on each side
	if inner < 20 {
		return rows
	}

	out := slices.Clone(rows)
	row := len(rows) - n*toastRows
	for _, c := range lines {
		// Failures are red on a red tint; warnings yellow, without one.
		border, tint := m.st.err, m.st.alert
		if c.warn {
			border, tint = m.st.warn, ""
		}
		box := func(r int, s string) {
			left := pad(ansi.Truncate(out[r], width-inner-4, ""), width-inner-4)
			if tint != "" {
				s = onBackground(s, tint)
			}
			out[r] = left + "\x1b[m " + s + " "
		}
		side := border.Render("│")
		box(row, border.Render("╭"+strings.Repeat("─", inner)+"╮"))
		box(row+1, side+pad(ansi.Truncate(c.title, inner, "…"), inner)+side)
		box(row+2, side+pad(ansi.Truncate(c.hint, inner, "…"), inner)+side)
		box(row+3, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
		row += toastRows
	}
	return out
}
