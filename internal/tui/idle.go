package tui

import (
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// idleHeading separates the session's projects from the ones not started.
const idleHeading = "not running"

// refreshIdle updates the list of registered projects that aren't in the
// session, shown below the session's in the sidebar when showAll is set.
// The selection stays on the same project.
func (m *runningModel) refreshIdle() {
	name := m.selectedName()
	m.idle, m.idleWidth = nil, 0
	if m.showAll {
		for _, n := range slices.Sorted(maps.Keys(m.cfg.Projects)) {
			if m.byName[n] == nil {
				m.idle = append(m.idle, n)
				m.idleWidth = max(m.idleWidth, ansi.StringWidth(m.labelOf(n)))
			}
		}
	}
	if i := m.indexOf(name); i >= 0 {
		m.selected = i
	}
	m.selected = max(min(m.selected, m.rows()-1), 0)
}

// rows is the number of selectable sidebar rows: the session's projects,
// then the ones not started.
func (m *runningModel) rows() int {
	return len(m.entries) + len(m.idle)
}

// indexOf returns the sidebar index of a project, or -1.
func (m *runningModel) indexOf(name string) int {
	if i := slices.IndexFunc(m.entries, func(e *sidebarEntry) bool { return e.info.Name == name }); i >= 0 {
		return i
	}
	if i := slices.Index(m.idle, name); i >= 0 {
		return len(m.entries) + i
	}
	return -1
}

// selectedIdle returns the selected project if it hasn't been started, or "".
func (m *runningModel) selectedIdle() string {
	if i := m.selected - len(m.entries); i >= 0 && i < len(m.idle) {
		return m.idle[i]
	}
	return ""
}

// selectedName returns the selected project, started or not, or "".
func (m *runningModel) selectedName() string {
	if e := m.selectedEntry(); e != nil {
		return e.info.Name
	}
	return m.selectedIdle()
}

// labelOf returns how a project is shown, without adding it to the sidebar.
func (m *runningModel) labelOf(name string) string {
	if e := m.byName[name]; e != nil {
		return e.label
	}
	if p := m.cfg.Projects[name]; p != nil {
		return p.Label(name)
	}
	return name
}

// styleOf returns the style of a project's label: its color once it is in
// the session, faint before.
func (m *runningModel) styleOf(name string) lipgloss.Style {
	if e := m.byName[name]; e != nil {
		return e.style
	}
	return m.st.faint
}

// startIdle starts a project that isn't in the session, with its
// dependencies, as if it was picked with +.
func (m *runningModel) startIdle(name string) {
	if !m.stopping {
		m.startPicked(pickerItem{name: name, label: m.labelOf(name)})
	}
}

// toggleAll shows or hides the projects not started.
func (m *runningModel) toggleAll() {
	m.showAll = !m.showAll
	m.refreshIdle()
	m.layout()
}

// idleRows returns how many sidebar rows of h go to the projects not
// started, with their heading: what's left after the session's projects,
// which are never pushed off screen by them. When one of them is selected,
// it gets a row (and the heading) regardless.
func (m *runningModel) idleRows(h int) int {
	if len(m.idle) == 0 {
		return 0
	}
	rows := min(len(m.idle)+1, h-len(m.entries)*m.entryLines())
	if m.selectedIdle() != "" {
		rows = max(rows, min(2, h))
	}
	if rows < 2 {
		return 0 // a heading alone says nothing
	}
	return rows
}

// idleHeadingRow renders the heading above the projects not started.
func (m *runningModel) idleHeadingRow(cols sidebarCols) string {
	return pad(ansi.Truncate(" "+m.st.faint.Render(idleHeading), cols.width, "…"), cols.width)
}

// idleRow renders a project that isn't in the session: a faint ○ and name,
// and its git state, also faint.
func (m *runningModel) idleRow(i int, cols sidebarCols) string {
	full, width := cols.width, cols.width
	if i >= len(m.idle) {
		return strings.Repeat(" ", width)
	}
	if cols.git > 0 {
		width -= cols.git + 1
	}
	name := m.idle[i]
	selected := m.selected == len(m.entries)+i
	marker, style := " ", m.st.faint
	if selected {
		marker, style = m.st.faint.Render("▌"), m.st.bold
		if m.st.selBar == "" {
			style = style.Reverse(true)
		}
	}
	label := ansi.Truncate(m.labelOf(name), width-m.sidebarPrefix()-1, "…")
	icon := m.st.faint.Render("○")
	row := " " + marker + " " + icon + " " + style.Render(label)
	if m.panes() {
		row = " " + marker + icon + " " + style.Render(label)
	}
	g, hasGit := m.git.get(name)
	switch {
	case !hasGit:
	case cols.git > 0:
		row = pad(row, width)
		row += m.st.faint.Render(ansi.Strip(gitLabel(m.st, g, cols.git)))
	case cols.twoLine:
		// One line each, so the list doesn't double in height: the git
		// label goes after the names, aligned, if there is room.
		at := m.sidebarPrefix() + m.idleWidth + 1
		if room := full - at - 1; room >= minGitWidth {
			row = pad(row, at) + m.st.faint.Render(ansi.Strip(gitLabel(m.st, g, min(room, maxGitWidth))))
		}
	}
	row = pad(row, full)
	if selected && m.st.selBar != "" {
		row = onBackground(row, m.st.selBar)
	}
	return row
}

// sidebarRows renders the sidebar's h rows: the session's projects from
// sideTop (entryLines rows each), then the heading and the projects not
// started from idleTop.
func (m *runningModel) sidebarRows(h int, cols sidebarCols) []string {
	idle := m.idleRows(h)
	session := min(len(m.entries)-m.sideTop, (h-idle)/m.entryLines())
	rows := make([]string, 0, h)
	for r := range session {
		rows = append(rows, m.sidebarRow(m.sideTop+r, cols)...)
	}
	if idle > 0 {
		rows = append(rows, m.idleHeadingRow(cols))
		for r := range idle - 1 {
			rows = append(rows, m.idleRow(m.idleTop+r, cols))
		}
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", cols.width))
	}
	return rows
}
