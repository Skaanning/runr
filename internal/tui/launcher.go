package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
)

// launcherItem is a group or project in the launcher list.
type launcherItem struct {
	name  string
	group bool
}

// startMsg asks the app to start the given groups and projects.
type startMsg struct{ names []string }

// launcherModel lists groups and projects, lets the user pick what to start,
// and edits the registry through forms.
type launcherModel struct {
	cfg      *config.Config
	save     SaveFunc       // nil: changes are only kept in memory
	items    []launcherItem // groups first, then projects, each sorted
	selected map[string]bool
	missing  map[string]bool // projects whose dir does not exist
	cursor   int             // index into items
	top      int             // first row shown, see rows
	notice   string          // shown instead of the help line until the next key
	form     *editForm       // open project or group form, or nil
	confirm  *confirmation   // open delete confirmation, or nil

	git      *gitTracker // nil: git is not installed
	gitStale bool        // the config changed; refresh the git state

	width, height int
}

func newLauncherModel(cfg *config.Config, save SaveFunc) *launcherModel {
	m := &launcherModel{cfg: cfg, save: save, selected: map[string]bool{}}
	m.reload()
	return m
}

// reload rebuilds the list from the config.
func (m *launcherModel) reload() {
	m.items = m.items[:0]
	for _, g := range slices.Sorted(maps.Keys(m.cfg.Groups)) {
		m.items = append(m.items, launcherItem{name: g, group: true})
	}
	for _, p := range slices.Sorted(maps.Keys(m.cfg.Projects)) {
		m.items = append(m.items, launcherItem{name: p})
	}
	m.missing = map[string]bool{}
	warnings, _ := m.cfg.Validate()
	for _, w := range warnings {
		if w.Field == "dir" {
			m.missing[w.Name] = true
		}
	}
	for p := range m.selected {
		if m.cfg.Projects[p] == nil {
			delete(m.selected, p)
		}
	}
	m.cursor = max(min(m.cursor, len(m.items)-1), 0)
}

func (m *launcherModel) Init() tea.Cmd {
	return m.git.refresh(m.gitDirs())
}

func (m *launcherModel) gitDirs() map[string]string {
	return projectDirs(m.cfg, slices.Collect(maps.Keys(m.cfg.Projects)))
}

func (m *launcherModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case gitMsg, gitTickMsg:
		return m, m.git.update(msg, m.gitDirs)
	}
	_, cmd := m.update(msg)
	if m.gitStale {
		m.gitStale = false
		cmd = tea.Batch(cmd, m.git.refresh(m.gitDirs()))
	}
	return m, cmd
}

func (m *launcherModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sizeForm()
	case interruptMsg:
		return m, tea.Quit
	}
	switch {
	case m.form != nil:
		return m, m.updateForm(msg)
	case m.confirm != nil:
		return m, m.updateConfirm(msg)
	}
	switch msg := msg.(type) {
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
		case "n":
			return m, m.openForm(newProjectForm(m.cfg, ""))
		case "e":
			if it, ok := m.current(); ok && it.group {
				return m, m.openForm(newGroupForm(m.cfg, it.name, nil))
			} else if ok {
				return m, m.openForm(newProjectForm(m.cfg, it.name))
			}
		case "g":
			if it, ok := m.current(); ok && it.group {
				return m, m.openForm(newGroupForm(m.cfg, it.name, nil))
			}
			return m, m.openForm(newGroupForm(m.cfg, "", slices.Sorted(maps.Keys(m.selected))))
		case "d":
			if it, ok := m.current(); ok {
				m.confirm = newDeleteConfirmation(m.cfg, it)
			}
		}
	}
	return m, nil
}

// current returns the item under the cursor.
func (m *launcherModel) current() (launcherItem, bool) {
	if m.cursor < len(m.items) {
		return m.items[m.cursor], true
	}
	return launcherItem{}, false
}

func (m *launcherModel) openForm(f *editForm) tea.Cmd {
	m.form = f
	m.sizeForm()
	return f.form.Init()
}

func (m *launcherModel) sizeForm() {
	if m.form != nil && m.width > 0 {
		m.form.form.WithWidth(max(m.width-2, 10)).WithHeight(max(m.height-4, 3))
	}
}

func (m *launcherModel) updateForm(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		m.notice = ""
		switch k.String() {
		case "ctrl+c":
			return tea.Quit
		case "esc":
			m.form = nil
			return nil
		}
	}
	f, cmd := m.form.form.Update(msg)
	m.form.form = f.(*huh.Form)
	switch m.form.form.State {
	case huh.StateCompleted:
		return m.submitForm()
	case huh.StateAborted:
		m.form = nil
	}
	return cmd
}

// submitForm saves a completed form, or reopens it with the error shown.
func (m *launcherModel) submitForm() tea.Cmd {
	f := m.form
	if !f.confirmed() {
		m.form = nil
		return nil
	}
	c, focus, err := f.result()
	if err == nil {
		_, err = c.Validate()
	}
	if err == nil && m.save != nil {
		err = m.save(c)
	}
	if err != nil {
		m.notice = oneLine(err.Error())
		f.form = f.rebuild()
		m.sizeForm()
		return f.form.Init()
	}
	m.form = nil
	renamed := f.original.name != "" && f.original.name != focus.name
	if renamed && m.selected[f.original.name] {
		delete(m.selected, f.original.name)
		m.selected[focus.name] = true
	}
	m.apply(c)
	if i := slices.Index(m.items, focus); i >= 0 {
		m.cursor = i
	}
	kind := "project"
	if focus.group {
		kind = "group"
	}
	m.notice = fmt.Sprintf("saved %s %q", kind, focus.name)
	if renamed {
		m.notice += fmt.Sprintf(" (renamed from %q)", f.original.name)
	}
	return nil
}

func (m *launcherModel) updateConfirm(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "ctrl+c":
		return tea.Quit
	case "y", "enter":
		c := m.confirm.result()
		m.confirm = nil
		if m.save != nil {
			if err := m.save(c); err != nil {
				m.notice = oneLine(err.Error())
				return nil
			}
		}
		m.apply(c)
	case "n", "esc":
		m.confirm = nil
	}
	return nil
}

// apply replaces the config in place, so everything sharing it sees the
// change, and rebuilds the list.
func (m *launcherModel) apply(c *config.Config) {
	*m.cfg = *c
	m.reload()
	m.gitStale = true // dirs may have changed
}

// oneLine joins a multi-line message, e.g. a list of validation errors.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
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

	if m.form != nil {
		rows = strings.Split(m.form.form.View(), "\n")
		for i, r := range rows {
			rows[i] = " " + r
		}
		m.top = 0
	}
	if m.confirm != nil {
		rows = []string{""}
		for _, l := range m.confirm.lines {
			rows = append(rows, "  "+l)
		}
		m.top = 0
	}

	var b strings.Builder
	right := ""
	if n := len(m.selected); n > 0 {
		right = fmt.Sprintf("%d selected ", n)
	}
	left := " " + boldStyle.Render("runr")
	if m.form != nil {
		left += " · " + m.form.title
	}
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
			"  No projects yet. Press n to add one, or run:",
			"",
			"    runr add <name> --dir <path> -- <command>",
		}, 0
	}
	nameWidth, dirWidth, gitCol := 0, 0, 0
	for _, it := range m.items {
		nameWidth = max(nameWidth, ansi.StringWidth(it.name))
		if !it.group {
			dirWidth = max(dirWidth, ansi.StringWidth(config.DisplayPath(m.cfg.Projects[it.name].Dir)))
			if g, ok := m.git.get(it.name); ok {
				gitCol = max(gitCol, gitWidth(g))
			}
		}
	}
	nameWidth, dirWidth, gitCol = min(nameWidth, 24), min(dirWidth, 32), min(gitCol, maxGitWidth)

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
		rows = append(rows, m.itemRow(it, i == m.cursor, nameWidth, dirWidth, gitCol))
	}
	return rows, cursorRow
}

func (m *launcherModel) itemRow(it launcherItem, current bool, nameWidth, dirWidth, gitCol int) string {
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
	row += dir + "  "
	if gitCol > 0 {
		label := ""
		if g, ok := m.git.get(it.name); ok {
			label = gitLabel(g, gitCol)
		}
		row += pad(label, gitCol) + "  "
	}
	return row + faintStyle.Render(details)
}

func (m *launcherModel) help() string {
	if m.notice != "" {
		return ansi.Truncate(" "+warnStyle.Render(m.notice), m.width, "…")
	}
	if m.form != nil {
		if errs := m.form.form.Errors(); len(errs) > 0 {
			return ansi.Truncate(" "+errStyle.Render("✗ "+oneLine(errs[0].Error())), m.width, "…")
		}
	}
	keys := [][2]string{
		{"↑↓", "move"}, {"space", "select"}, {"enter", "start"},
		{"n", "new"}, {"e", "edit"}, {"g", "group"}, {"d", "delete"}, {"q", "quit"},
	}
	switch {
	case m.form != nil:
		keys = [][2]string{{"tab/enter", "next"}, {"shift+tab", "back"}, {"space", "toggle"}, {"esc", "cancel"}}
	case m.confirm != nil:
		keys = [][2]string{{"y", "delete"}, {"n", "cancel"}}
	}
	return ansi.Truncate(" "+hintList(keys), m.width, "…")
}
