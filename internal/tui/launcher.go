package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/gitinfo"
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
	st       *styles
	save     SaveFunc       // nil: changes are only kept in memory
	items    []launcherItem // groups first, then projects, each sorted
	selected map[string]bool
	missing  map[string]bool // projects whose dir does not exist
	cursor   int             // index into items
	top      int             // first row shown, see rows
	notice   string          // shown instead of the help line until the next key
	form     *editForm       // open project or group form, or nil
	confirm  *confirmation   // open delete confirmation, or nil
	picker   *picker         // open branch or theme picker, or nil
	ask      *question       // open question in the help line (pull?), or nil
	output   []string        // output of a failed git action, shown until the next key

	git       *gitTracker // nil: git is not installed
	gitStale  bool        // the config or a repository changed; refresh the git state
	gitAction string      // git action running in the background, e.g. "git switch"

	width, height int
	now           func() time.Time // for the clock
}

func newLauncherModel(cfg *config.Config, save SaveFunc, st *styles) *launcherModel {
	m := &launcherModel{cfg: cfg, st: st, save: save, selected: map[string]bool{}, now: time.Now}
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
	return tea.Batch(m.git.refresh(m.gitDirs()), tea.RequestBackgroundColor, clockTick(m, m.now()))
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
	case tea.ColorProfileMsg:
		m.st.setProfile(msg.Profile)
	case tea.BackgroundColorMsg:
		m.st.setDark(msg.IsDark())
	case clockMsg:
		if msg.owner == m {
			return m, clockTick(m, m.now())
		}
		return m, nil
	case interruptMsg:
		return m, tea.Quit
	case branchesMsg:
		if msg.owner == m {
			return m, m.showBranches(msg)
		}
		return m, nil
	case switchedMsg:
		if msg.owner == m {
			m.switched(msg)
		}
		return m, nil
	case pullCheckMsg:
		if msg.owner == m {
			m.confirmPull(msg)
		}
		return m, nil
	case pulledMsg:
		if msg.owner == m {
			m.pulled(msg)
		}
		return m, nil
	}
	switch {
	case m.form != nil:
		return m, m.updateForm(msg)
	case m.confirm != nil:
		return m, m.updateConfirm(msg)
	case m.picker != nil:
		return m, m.updatePicker(msg)
	case m.ask != nil:
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			m.notice = ""
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			done, cmd := m.ask.answer(msg.String())
			if done {
				m.ask = nil
			}
			return m, cmd
		}
		return m, nil
	case m.output != nil:
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			m.output, m.notice = nil, ""
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
		}
		return m, nil
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
			if m.gitAction != "" {
				m.notice = m.gitAction + " is still running"
				return m, nil
			}
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
		case "b":
			return m, m.openBranches()
		case "p":
			return m, m.openPull()
		case "t":
			return m, m.openThemes()
		}
	}
	return m, nil
}

// openThemes opens the theme picker, on the current theme. The launcher is
// drawn in the theme under the cursor until one is picked or esc restores
// the saved one.
func (m *launcherModel) openThemes() tea.Cmd {
	current := m.cfg.Settings.Theme()
	items := make([]pickerItem, len(config.ThemeNames))
	for i, name := range config.ThemeNames {
		t := themeNamed(name)
		info := t.about
		if name == current {
			info += " · current"
		}
		items[i] = pickerItem{name: name, label: name, info: info}
		if m.st.tints() {
			items[i].swatch = m.st.swatch(t)
		}
	}
	p := &picker{
		title: "theme", action: "use", mode: "theme", items: items,
		pick:    func(it pickerItem) tea.Cmd { m.saveTheme(it.name); return nil },
		preview: func(it pickerItem) { m.st.setTheme(themeNamed(it.name)) },
		cancel:  m.restoreTheme,
	}
	if !m.st.tints() {
		p.note = "this terminal shows 16 colors, so runr uses the terminal theme here"
	}
	var cmd tea.Cmd
	m.picker, cmd = newPicker(m.st, p)
	m.picker.cursor = max(slices.Index(config.ThemeNames, current), 0)
	return cmd
}

// saveTheme makes the named theme the configured one.
func (m *launcherModel) saveTheme(name string) {
	c := *m.cfg // save only reads it
	c.Settings.ThemeValue = name
	if m.save != nil {
		if err := m.save(&c); err != nil {
			m.notice = "can't save the theme: " + oneLine(err.Error())
			m.restoreTheme()
			return
		}
	}
	m.cfg.Settings.ThemeValue = name
	m.st.setTheme(themeNamed(name))
	m.notice = "theme set to " + name
}

// restoreTheme draws the launcher in the configured theme again.
func (m *launcherModel) restoreTheme() {
	m.st.setTheme(themeNamed(m.cfg.Settings.Theme()))
}

// openBranches checks the repository of the project under the cursor and
// reads its branches in the background; the picker opens when they arrive.
func (m *launcherModel) openBranches() tea.Cmd {
	name, repo, ok := m.gitTarget("switch its branch")
	if !ok {
		return nil
	}
	return readBranches(m, m.git.reader, name, repo)
}

// openPull checks the repository of the project under the cursor in the
// background; the question whether to pull opens when it is done.
func (m *launcherModel) openPull() tea.Cmd {
	name, repo, ok := m.gitTarget("pull")
	if !ok {
		return nil
	}
	return checkPull(m, m.git.reader, name, repo)
}

// gitTarget returns the project under the cursor and its repository for a
// git action (what, e.g. "pull"). If there is none, or another action is
// running, it sets a notice saying why.
func (m *launcherModel) gitTarget(what string) (string, gitinfo.Repo, bool) {
	it, ok := m.current()
	if !ok {
		return "", gitinfo.Repo{}, false
	}
	if it.group {
		m.notice = "move to a project to " + what
		return "", gitinfo.Repo{}, false
	}
	repo, notice := m.git.target(it.name, it.name, m.gitAction)
	m.notice = notice
	return it.name, repo, notice == ""
}

// busy reports whether something else has the screen.
func (m *launcherModel) busy() bool {
	return m.form != nil || m.confirm != nil || m.picker != nil || m.ask != nil || m.output != nil
}

// showBranches opens the branch picker. Every registered project in the
// repository switches along.
func (m *launcherModel) showBranches(msg branchesMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = "can't switch branches: " + msg.err.Error()
		return nil
	}
	if m.busy() {
		return nil // something else took over the screen meanwhile
	}
	var cmd tea.Cmd
	m.picker, cmd = branchPicker(m.st, msg, msg.project, m.sharing(msg.project, msg.repo.Root), func(b gitinfo.Branch) tea.Cmd {
		return m.switchBranch(msg.project, msg.repo, b)
	})
	return cmd
}

// sharing returns the other registered projects in the repository at root.
func (m *launcherModel) sharing(project, root string) []string {
	return m.git.inRepo(project, root, slices.Sorted(maps.Keys(m.cfg.Projects)))
}

func (m *launcherModel) updatePicker(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.picker.input, cmd = m.picker.input.Update(msg)
		return cmd
	}
	m.notice = ""
	switch key.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		if m.picker.cancel != nil {
			m.picker.cancel()
		}
		m.picker = nil
	case "enter":
		if it, ok := m.picker.selected(); ok {
			pick := m.picker.pick
			m.picker = nil
			return pick(it)
		}
	default:
		return m.picker.update(key)
	}
	return nil
}

// switchBranch switches the repository of project to b in the background,
// after checking it again.
func (m *launcherModel) switchBranch(project string, repo gitinfo.Repo, b gitinfo.Branch) tea.Cmd {
	if b.Current {
		m.notice = "already on " + b.Name
		return nil
	}
	if m.gitAction != "" {
		m.notice = m.gitAction + " is still running"
		return nil
	}
	m.gitAction = "git switch"
	m.notice = fmt.Sprintf("switching %s to %s…", project, b.LocalName())
	return runSwitch(m, m.git.reader, project, repo, b)
}

// switched reports the outcome of a branch switch: a summary in the help
// line, and git's output in place of the list if it failed.
func (m *launcherModel) switched(msg switchedMsg) {
	m.gitAction = ""
	if !msg.ran {
		m.notice = "can't switch branches: " + msg.err.Error()
		return
	}
	m.gitStale = true
	if msg.err != nil {
		m.output = switchLines(msg)
		m.notice = msg.err.Error()
		return
	}
	m.notice = fmt.Sprintf("switched %s to %s", msg.project, msg.branch.LocalName())
	if others := m.sharing(msg.project, msg.repo.Root); len(others) > 0 {
		m.notice += " (also " + strings.Join(others, ", ") + ")"
	}
}

// confirmPull asks whether to pull, naming the other registered projects in
// the repository.
func (m *launcherModel) confirmPull(msg pullCheckMsg) {
	if msg.err != nil {
		m.notice = "can't pull: " + msg.err.Error()
		return
	}
	if m.busy() {
		return
	}
	m.notice = ""
	m.ask = pullQuestion(msg, msg.project, m.sharing(msg.project, msg.repo.Root), func() tea.Cmd {
		return m.pull(msg.project, msg.repo)
	})
}

// pull pulls the repository of project in the background, after checking
// it again.
func (m *launcherModel) pull(project string, repo gitinfo.Repo) tea.Cmd {
	if m.gitAction != "" {
		m.notice = m.gitAction + " is still running"
		return nil
	}
	m.gitAction = "git pull"
	m.notice = fmt.Sprintf("pulling %s…", project)
	return runPull(m, m.git.reader, project, repo)
}

// pulled reports the outcome of a pull: a summary in the help line, and
// git's output in place of the list if it failed.
func (m *launcherModel) pulled(msg pulledMsg) {
	m.gitAction = ""
	if !msg.ran {
		m.notice = "can't pull: " + msg.err.Error()
		return
	}
	m.gitStale = true
	if msg.err != nil {
		m.output = pullLines(msg)
		m.notice = msg.err.Error()
		return
	}
	switch change := pullChange(msg); {
	case change == "":
		m.notice = msg.project + " is already up to date"
	case msg.after.Commit == "":
		m.notice = fmt.Sprintf("pulled %s into %s", change, msg.project)
	default:
		m.notice = fmt.Sprintf("pulled %s: %s", msg.project, change)
		if others := m.sharing(msg.project, msg.repo.Root); len(others) > 0 {
			m.notice += " (also " + strings.Join(others, ", ") + ")"
		}
	}
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
	f.form.WithTheme(m.st.formTheme())
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
		f.form = f.rebuild().WithTheme(m.st.formTheme())
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
	if m.picker != nil {
		rows = m.picker.view(m.width-2, body)
		for i, r := range rows {
			rows[i] = " " + r
		}
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
	left := " " + m.st.badge
	if m.form != nil {
		left += "  " + m.st.bold.Render(m.form.title)
	}
	b.WriteString(ansi.Truncate(left, m.width, "…"))
	b.WriteString("\n" + m.st.rule.Render(strings.Repeat("─", m.width)) + "\n")
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
	case m.form != nil:
		b.mode = strings.ToUpper(m.form.title)
	case m.confirm != nil:
		b.mode, b.color = "DELETE", m.st.pal.ansi[ansiRed]
	case m.output != nil:
		b.mode, b.color = "FAILED", m.st.pal.ansi[ansiRed]
	case m.picker != nil:
		b.mode, b.color = m.picker.modeName(), m.st.pal.ansi[ansiBlue]
	case m.ask != nil:
		b.mode, b.color = strings.ToUpper(m.ask.action), m.st.pal.ansi[ansiBlue]
	}
	b.info = m.st.faint.Render(count(len(m.cfg.Projects), "project") + " · " + count(len(m.cfg.Groups), "group"))
	if n := len(m.selected); n > 0 {
		b.info += m.st.faint.Render(" · ") + fmt.Sprintf("%d selected", n)
	}
	if it, ok := m.current(); ok && !it.group {
		style := m.st.bold
		if c := m.cfg.Projects[it.name].Color; c != "" {
			style = style.Foreground(m.st.color(c))
		}
		b.project = style.Render(it.name)
		if g, ok := m.git.get(it.name); ok {
			b.project += m.st.faint.Render(" · ") + gitLabel(m.st, g, gitWidth(g))
		}
	}
	return m.st.statusBar(b, m.width)
}

// count returns "1 project", "2 projects".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
			rows = append(rows, "  "+m.st.faint.Render(h))
		}
		if i == m.cursor {
			cursorRow = len(rows)
		}
		rows = append(rows, m.itemRow(it, i == m.cursor, nameWidth, dirWidth, gitCol))
	}
	return rows, cursorRow
}

func (m *launcherModel) itemRow(it launcherItem, current bool, nameWidth, dirWidth, gitCol int) string {
	marker, nameStyle := "  ", m.st.plain
	if current {
		marker, nameStyle = "▸ ", m.st.bold
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
			return row + m.st.faint.Render("(empty)")
		}
		return row + m.st.faint.Render(strings.Join(members, ", "))
	}

	p := m.cfg.Projects[it.name]
	dir := pad(ansi.Truncate(config.DisplayPath(p.Dir), dirWidth, "…"), dirWidth)
	if m.missing[it.name] {
		dir = m.st.err.Render(dir)
	} else {
		dir = m.st.faint.Render(dir)
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
			label = gitLabel(m.st, g, gitCol)
		}
		row += pad(label, gitCol) + "  "
	}
	return row + m.st.faint.Render(details)
}

func (m *launcherModel) help() string {
	if m.ask != nil {
		return questionLine(m.st, m.ask, m.width)
	}
	if m.notice != "" {
		return ansi.Truncate(" "+m.st.warn.Render(m.notice), m.width, "…")
	}
	if m.picker != nil {
		return ansi.Truncate(" "+m.st.hintList([][2]string{
			{"type", "to filter"}, {"↑↓", "select"}, {"enter", m.picker.action}, {"esc", "cancel"},
		}), m.width, "…")
	}
	if m.form != nil {
		if errs := m.form.form.Errors(); len(errs) > 0 {
			return ansi.Truncate(" "+m.st.err.Render("✗ "+oneLine(errs[0].Error())), m.width, "…")
		}
	}
	keys := [][2]string{
		{"↑↓", "move"}, {"space", "select"}, {"enter", "start"},
		{"n", "new"}, {"e", "edit"}, {"g", "group"}, {"d", "delete"},
	}
	if it, ok := m.current(); ok && !it.group {
		if _, ok := m.git.get(it.name); ok {
			keys = append(keys, [2]string{"b", "branch"}, [2]string{"p", "pull"})
		}
	}
	keys = append(keys, [2]string{"t", "theme"}, [2]string{"q", "quit"})
	switch {
	case m.form != nil:
		keys = [][2]string{{"tab/enter", "next"}, {"shift+tab", "back"}, {"space", "toggle"}, {"esc", "cancel"}}
	case m.confirm != nil:
		keys = [][2]string{{"y", "delete"}, {"n", "cancel"}}
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = m.st.hint(k[0], k[1])
	}
	return m.st.fitHints(parts, m.width)
}
