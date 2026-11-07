package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
)

// Drawing the launcher: the group cards and project list, the status bar
// and the help line.

func (m *launcherModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "runr"
	v.ForegroundColor, v.BackgroundColor = m.st.screen()
	return v
}

func (m *launcherModel) render() string {
	if m.width == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return "terminal too small"
	}
	var header []string
	switch {
	case m.form != nil: // the form's pane has the title
	case m.showWordmark():
		header = m.wordmarkRows()
	default:
		header = []string{" " + m.st.badge, m.st.rule.Render(strings.Repeat("─", m.width))}
	}
	body := m.height - len(header) - 2
	rows, first, last := m.rows()
	switch {
	case first < m.top:
		m.top = first
	case last >= m.top+body:
		m.top = max(last-body+1, first)
	}
	m.top = max(min(m.top, len(rows)-body), 0)

	if m.form != nil {
		fr := m.form.form.view(m.width-4, max(body-2, 1))
		for len(fr) < body-2 {
			fr = append(fr, "")
		}
		rows = framed(m.st, m.st.heading.Render(m.form.title), m.st.faint.Render("ctrl+s "+m.form.form.actionName()), fr, m.width)
		m.top = 0
	}
	if m.confirm != nil {
		rows = []string{""}
		for _, l := range m.confirm.lines {
			rows = append(rows, "  "+l)
		}
		m.top = 0
	}
	if m.picker != nil {
		rows = framed(m.st, m.picker.titleText(), "", m.picker.view(m.width-4, max(body-2, 0)), m.width)
		m.top = 0
	}
	if m.output != nil {
		rows = []string{""}
		for _, l := range m.output {
			rows = append(rows, "  "+sanitize(l))
		}
		rows = append(rows, "", "  "+m.st.faint.Render("press any key to close"))
		// Keep the end of the output, where git says what went wrong.
		m.top = max(len(rows)-body, 0)
	}

	var b strings.Builder
	for _, r := range header {
		b.WriteString(ansi.Truncate(r, m.width, "…") + "\n")
	}
	for r := range body {
		if i := m.top + r; i < len(rows) {
			b.WriteString(ansi.Truncate(rows[i], m.width, "…"))
		}
		b.WriteByte('\n')
	}
	b.WriteString(m.statusBar() + "\n")
	b.WriteString(m.help())
	return b.String()
}

// statusBar renders what the launcher is doing, how many projects and groups
// there are, and the project under the cursor with its git state.
func (m *launcherModel) statusBar() string {
	b := statusBar{mode: "SELECT", color: m.st.pal.accent, clock: m.now().Format("15:04")}
	switch {
	case m.form != nil && m.picker != nil:
		b.mode, b.color = m.picker.modeName(), m.st.pal.ansi[ansiBlue]
		b.project = m.form.context()
		return m.st.statusBar(b, m.width)
	case m.form != nil:
		b.mode = strings.ToUpper(m.form.title)
		b.info, b.project = m.form.statusInfo(m.st), m.form.context()
		return m.st.statusBar(b, m.width)
	case m.confirm != nil:
		b.mode, b.color = "DELETE", m.st.pal.ansi[ansiRed]
	case m.output != nil && m.skipped:
		b.mode, b.color = "SKIPPED", m.st.pal.ansi[ansiYellow]
	case m.output != nil:
		b.mode, b.color = "FAILED", m.st.pal.ansi[ansiRed]
	case m.picker != nil:
		b.mode, b.color = m.picker.modeName(), m.st.pal.ansi[ansiBlue]
	case m.ask != nil:
		b.mode, b.color = strings.ToUpper(m.ask.action), m.st.pal.ansi[ansiBlue]
	case m.jump:
		b.mode, b.color = "JUMP", m.st.pal.ansi[ansiBlue]
	}
	b.info = m.st.faint.Render(m.registrySize())
	if n := len(m.selected); n > 0 {
		b.info += m.st.faint.Render(" · ") + fmt.Sprintf("%d selected", n)
	}
	if it, ok := m.current(); ok && !it.group {
		b.project = m.st.bold.Foreground(m.st.color(m.cfg.ColorOf(it.name))).Render(it.name)
		if g, ok := m.git.get(it.name); ok {
			b.project += m.st.faint.Render(" · ") + gitLabel(m.st, g, gitWidth(g))
		}
	}
	return m.st.statusBar(b, m.width)
}

// rows renders the group cards and the project list with section headings,
// and returns the first and last row to keep in view for the cursor: its
// card or row, and the heading above it if it's the first in its section.
func (m *launcherModel) rows() (rows []string, first, last int) {
	if len(m.items) == 0 {
		return []string{
			"",
			"  No projects yet. Press n to add one, or run:",
			"",
			"    runr add <name> --dir <path> -- <command>",
		}, 0, 0
	}
	indent := strings.Repeat(" ", listIndent)
	groups := m.groupCount()
	if groups > 0 {
		rows = append(rows, indent+m.st.heading.Render("Groups"))
		cards, cardFirst, cardLast := m.cardRows()
		if cardFirst >= 0 {
			first, last = len(rows)+cardFirst, len(rows)+cardLast
			if cardFirst == 0 {
				first = 0
			}
		}
		rows = append(rows, cards...)
	}
	if groups == len(m.items) {
		return rows, first, last
	}
	if groups > 0 {
		rows = append(rows, "")
	}
	heading := len(rows)
	rows = append(rows, indent+m.st.heading.Render("Projects"))

	nameWidth, dirWidth, gitCol := 0, 0, 0
	for _, it := range m.items[groups:] {
		nameWidth = max(nameWidth, ansi.StringWidth(it.name))
		dirWidth = max(dirWidth, ansi.StringWidth(config.DisplayPath(m.cfg.Projects[it.name].Dir)))
		if g, ok := m.git.get(it.name); ok {
			gitCol = max(gitCol, gitWidth(g))
		}
	}
	nameWidth, dirWidth, gitCol = m.projectColumns(nameWidth, dirWidth, gitCol)
	for i, it := range m.items[groups:] {
		if groups+i == m.cursor {
			first, last = len(rows), len(rows)
			if i == 0 {
				first = heading
			}
		}
		rows = append(rows, m.projectRow(it.name, m.jumpLabelAt(groups+i), groups+i == m.cursor, nameWidth, dirWidth, gitCol))
	}
	return rows, first, last
}

// projectColumns returns the widths of the name, directory and git columns,
// given their widest values: capped, and narrowed further when the screen is
// too narrow to leave the command minCmdWidth cells, the directory first.
func (m *launcherModel) projectColumns(nameWidth, dirWidth, gitCol int) (int, int, int) {
	nameWidth, dirWidth, gitCol = min(nameWidth, 24), min(dirWidth, maxDirWidth), min(gitCol, maxLauncherGitWidth)
	room := m.width - listIndent - ansi.StringWidth("[ ] ● ") - nameWidth - 2 - 2 - minCmdWidth
	if gitCol > 0 {
		room -= 2
	}
	over := dirWidth + gitCol - room
	if over > 0 {
		cut := min(over, dirWidth-min(dirWidth, minDirWidth))
		dirWidth, over = dirWidth-cut, over-cut
	}
	if over > 0 {
		gitCol -= min(over, gitCol-min(gitCol, minGitWidth))
	}
	return nameWidth, dirWidth, gitCol
}

// shortenPath fits a path into width cells by replacing the directories after
// its first element with "…", "~/…/client/api", dropping as few as it can. A
// path whose last element alone is too wide keeps the end of it.
func shortenPath(path string, width int) string {
	if ansi.StringWidth(path) <= width {
		return path
	}
	var seps []int
	for i, r := range path {
		if r == '/' || r == filepath.Separator {
			seps = append(seps, i)
		}
	}
	if len(seps) > 1 {
		head := path[:seps[0]+1]
		for _, i := range seps[1:] {
			if s := head + "…" + path[i:]; ansi.StringWidth(s) <= width {
				return s
			}
		}
	}
	if len(seps) > 0 {
		if s := "…" + path[seps[len(seps)-1]:]; ansi.StringWidth(s) <= width {
			return s
		}
	}
	if width < 1 {
		return ""
	}
	return "…" + ansi.TruncateLeft(path, ansi.StringWidth(path)-width+1, "")
}

// projectRow renders a project: checkbox, color dot and name, directory, git
// label, command and dependencies. A jump mode letter (label) takes the
// place of the cursor marker.
func (m *launcherModel) projectRow(name, label string, current bool, nameWidth, dirWidth, gitCol int) string {
	marker, nameStyle := strings.Repeat(" ", listIndent), lipgloss.NewStyle()
	if current {
		marker, nameStyle = " ▸ ", m.st.bold
	}
	if label != "" {
		marker = " " + m.st.accent.Bold(true).Render(label) + " "
	}
	color := m.st.color(m.cfg.ColorOf(name))
	row := marker + m.checkbox([]string{name}) + " " + lipgloss.NewStyle().Foreground(color).Render("●") + " " +
		nameStyle.Foreground(color).Render(pad(ansi.Truncate(name, nameWidth, "…"), nameWidth)) + "  "

	p := m.cfg.Projects[name]
	dir := pad(shortenPath(config.DisplayPath(p.Dir), dirWidth), dirWidth)
	if m.missing[name] {
		dir = m.st.err.Render(dir)
	} else {
		dir = m.st.faint.Render(dir)
	}
	row += dir + "  "
	if gitCol > 0 {
		label := ""
		if g, ok := m.git.get(name); ok {
			label = gitLabel(m.st, g, gitCol)
		}
		row += pad(label, gitCol) + "  "
	}
	switch {
	case m.missing[name]:
		return row + m.st.faint.Render("dir does not exist")
	case len(p.DependsOn) > 0:
		return row + m.st.command.Render(p.Cmd) + m.st.faint.Render(" · needs "+strings.Join(p.DependsOn, ", "))
	}
	return row + m.st.command.Render(p.Cmd)
}

func (m *launcherModel) help() string {
	if m.ask != nil {
		return questionLine(m.st, m.ask, m.width)
	}
	if m.notice != "" {
		return ansi.Truncate(" "+m.st.warn.Render(m.notice), m.width, "…")
	}
	if m.picker != nil {
		keys := m.picker.hints
		if keys == nil {
			keys = [][2]string{{"type", "to filter"}, {"↑↓", "select"}, {"enter", m.picker.action}, {"esc", "cancel"}}
		}
		return ansi.Truncate(" "+m.st.hintList(keys), m.width, "…")
	}
	if m.jump {
		keys := [][2]string{{"letter", "start group · select project"}, {"enter", "start selected"}, {"esc", "done"}}
		return ansi.Truncate(" "+m.st.hintList(keys), m.width, "…")
	}
	move := "↑↓"
	if m.groupCount() > 1 {
		move = "↑↓←→"
	}
	keys := [][2]string{
		{move, "move"}, {"space", "select"}, {"enter", "start"},
		{"n", "new"}, {"e", "edit"}, {"g", "group"}, {"d", "delete"},
	}
	if it, ok := m.current(); ok && !it.group {
		if _, ok := m.git.get(it.name); ok {
			keys = append(keys, [2]string{"b", "branch"}, [2]string{"p", "pull"})
		}
	}
	if m.git != nil && len(m.git.repos.ByRoot) > 0 {
		keys = append(keys, [2]string{"F", "fetch all"}, [2]string{"P", "pull all"})
	}
	if it, ok := m.current(); ok && !it.group {
		keys = append(keys, [2]string{"c", "copy cmd"})
	}
	if len(m.items) > 0 {
		keys = append(keys, [2]string{"R", "args"})
	}
	keys = append(keys, [2]string{"t", "theme"}, [2]string{"/", "jump"}, [2]string{"q", "quit"})
	switch {
	case m.form != nil:
		return m.formHints()
	case m.confirm != nil:
		keys = [][2]string{{"y", "delete"}, {"n", "cancel"}}
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = m.st.hint(k[0], k[1])
	}
	return m.st.fitHints(parts, m.width)
}

// formHints renders the form's key hints. When narrow, shift+tab goes
// first, then tab, then hints from the middle.
func (m *launcherModel) formHints() string {
	keys := m.form.form.keyHints()
	for _, drop := range []string{"", "shift+tab", "tab"} {
		keys = slices.DeleteFunc(keys, func(k [2]string) bool { return k[0] == drop })
		if line := " " + m.st.hintList(keys); ansi.StringWidth(line) <= m.width || drop == "tab" {
			parts := make([]string, len(keys))
			for i, k := range keys {
				parts[i] = m.st.hint(k[0], k[1])
			}
			return m.st.fitHints(parts, m.width)
		}
	}
	return ""
}
