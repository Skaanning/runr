package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"runr/internal/config"
)

// launcherItem is a group or project in the launcher list.
type launcherItem struct {
	name  string
	group bool
}

// startMsg asks the app to start the given groups and projects.
type startMsg struct{ names []string }

// launcherModel lists groups and projects and lets the user pick what to
// start.
type launcherModel struct {
	cfg      *config.Config
	items    []launcherItem // groups first, then projects, each sorted
	selected map[string]bool
	missing  map[string]bool // projects whose dir does not exist
	cursor   int             // index into items
	top      int             // first row shown, see rows
	notice   string          // shown instead of the help line until the next key

	width, height int
}

func newLauncherModel(cfg *config.Config) *launcherModel {
	m := &launcherModel{cfg: cfg, selected: map[string]bool{}, missing: map[string]bool{}}
	for _, g := range slices.Sorted(maps.Keys(cfg.Groups)) {
		m.items = append(m.items, launcherItem{name: g, group: true})
	}
	for _, p := range slices.Sorted(maps.Keys(cfg.Projects)) {
		m.items = append(m.items, launcherItem{name: p})
	}
	warnings, _ := cfg.Validate()
	for _, w := range warnings {
		if w.Field == "dir" {
			m.missing[w.Name] = true
		}
	}
	return m
}

func (m *launcherModel) Init() tea.Cmd { return nil }

func (m *launcherModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case interruptMsg:
		return m, tea.Quit
	case tea.KeyPressMsg:
		m.notice = ""
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up":
			m.cursor = max(m.cursor-1, 0)
		case "down":
			m.cursor = max(min(m.cursor+1, len(m.items)-1), 0)
		case "home":
			m.cursor = 0
		case "end":
			m.cursor = max(len(m.items)-1, 0)
		case "space":
			m.toggle()
		case "enter":
			names := m.selection()
			if len(names) == 0 && m.cursor < len(m.items) {
				names = []string{m.items[m.cursor].name} // nothing selected: start the item under the cursor
			}
			if len(names) > 0 {
				return m, func() tea.Msg { return startMsg{names} }
			}
		}
	}
	return m, nil
}

// members returns the projects an item stands for.
func (m *launcherModel) members(it launcherItem) []string {
	if it.group {
		return m.cfg.Groups[it.name]
	}
	return []string{it.name}
}

// toggle selects the item under the cursor, or unselects it if all of its
// projects are already selected.
func (m *launcherModel) toggle() {
	if m.cursor >= len(m.items) {
		return
	}
	members := m.members(m.items[m.cursor])
	all := m.countSelected(members) == len(members)
	for _, p := range members {
		if all {
			delete(m.selected, p)
		} else {
			m.selected[p] = true
		}
	}
}

func (m *launcherModel) countSelected(projects []string) int {
	n := 0
	for _, p := range projects {
		if m.selected[p] {
			n++
		}
	}
	return n
}

// selection names what to start: groups whose members are all selected,
// then the other selected projects.
func (m *launcherModel) selection() []string {
	var names []string
	covered := map[string]bool{}
	for _, it := range m.items {
		members := m.members(it)
		switch {
		case it.group && len(members) > 0 && m.countSelected(members) == len(members):
			names = append(names, it.name)
			for _, p := range members {
				covered[p] = true
			}
		case !it.group && m.selected[it.name] && !covered[it.name]:
			names = append(names, it.name)
		}
	}
	return names
}

func (m *launcherModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "runr"
	return v
}

func (m *launcherModel) render() string {
	if m.width == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return "terminal too small"
	}
	body := m.height - 4
	rows, cursorRow := m.rows()
	switch {
	case cursorRow < m.top:
		m.top = cursorRow
		if m.top == 1 {
			m.top = 0 // keep the "Groups" heading in view
		}
	case cursorRow >= m.top+body:
		m.top = cursorRow - body + 1
	}
	m.top = max(min(m.top, len(rows)-body), 0)

	var b strings.Builder
	right := ""
	if n := len(m.selected); n > 0 {
		right = fmt.Sprintf("%d selected ", n)
	}
	left := " " + boldStyle.Render("runr")
	b.WriteString(left + strings.Repeat(" ", max(m.width-ansi.StringWidth(left)-len(right), 1)) + right)
	b.WriteString("\n" + ruleStyle.Render(strings.Repeat("─", m.width)) + "\n")
	for r := range body {
		if i := m.top + r; i < len(rows) {
			b.WriteString(ansi.Truncate(rows[i], m.width, "…"))
		}
		b.WriteByte('\n')
	}
	b.WriteString(ruleStyle.Render(strings.Repeat("─", m.width)) + "\n")
	b.WriteString(m.help())
	return b.String()
}

// rows renders the list with section headings and returns the row of the
// cursor.
func (m *launcherModel) rows() (rows []string, cursorRow int) {
	if len(m.items) == 0 {
		return []string{
			"",
			"  No projects yet. Register one with:",
			"",
			"    runr add <name> --dir <path> -- <command>",
		}, 0
	}
	nameWidth, dirWidth := 0, 0
	for _, it := range m.items {
		nameWidth = max(nameWidth, ansi.StringWidth(it.name))
		if !it.group {
			dirWidth = max(dirWidth, ansi.StringWidth(config.DisplayPath(m.cfg.Projects[it.name].Dir)))
		}
	}
	nameWidth, dirWidth = min(nameWidth, 24), min(dirWidth, 32)

	heading := ""
	for i, it := range m.items {
		if h := map[bool]string{true: "Groups", false: "Projects"}[it.group]; h != heading {
			if heading != "" {
				rows = append(rows, "")
			}
			heading = h
			rows = append(rows, "  "+faintStyle.Render(h))
		}
		if i == m.cursor {
			cursorRow = len(rows)
		}
		rows = append(rows, m.itemRow(it, i == m.cursor, nameWidth, dirWidth))
	}
	return rows, cursorRow
}

func (m *launcherModel) itemRow(it launcherItem, current bool, nameWidth, dirWidth int) string {
	marker, nameStyle := "  ", plainStyle
	if current {
		marker, nameStyle = "▸ ", boldStyle
	}
	members := m.members(it)
	check := "[ ]"
	switch n := m.countSelected(members); {
	case len(members) > 0 && n == len(members):
		check = "[x]"
	case n > 0:
		check = "[-]"
	}
	row := " " + marker + check + " " + nameStyle.Render(pad(ansi.Truncate(it.name, nameWidth, "…"), nameWidth)) + "  "
	if it.group {
		if len(members) == 0 {
			return row + faintStyle.Render("(empty)")
		}
		return row + faintStyle.Render(strings.Join(members, ", "))
	}

	p := m.cfg.Projects[it.name]
	dir := pad(ansi.Truncate(config.DisplayPath(p.Dir), dirWidth, "…"), dirWidth)
	if m.missing[it.name] {
		dir = errStyle.Render(dir)
	} else {
		dir = faintStyle.Render(dir)
	}
	details := p.Cmd
	if len(p.DependsOn) > 0 {
		details += " · needs " + strings.Join(p.DependsOn, ", ")
	}
	if m.missing[it.name] {
		details = "dir does not exist"
	}
	return row + dir + "  " + faintStyle.Render(details)
}

func (m *launcherModel) help() string {
	if m.notice != "" {
		return ansi.Truncate(" "+warnStyle.Render(m.notice), m.width, "…")
	}
	return ansi.Truncate(" "+hintList([][2]string{
		{"↑↓", "move"}, {"space", "select"}, {"enter", "start"}, {"q", "quit"},
	}), m.width, "…")
}
