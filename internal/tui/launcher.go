package tui

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/gitinfo"
)

const (
	maxDirWidth         = 60 // widest directory column; longer paths lose directories
	minDirWidth         = 16 // the directory column shrinks to this on narrow screens
	maxLauncherGitWidth = 40 // widest git column; longer branches are truncated
	minCmdWidth         = 20 // room left for the command before columns shrink
)

// launcherItem is a group or project in the launcher list.
type launcherItem struct {
	name  string
	group bool
}

// startMsg asks the app to start the given groups and projects, with extra
// arguments by project (blank: none).
type startMsg struct {
	names []string
	args  map[string]string
}

// launcherModel lists groups and projects, lets the user pick what to start,
// and edits the registry through forms.
type launcherModel struct {
	cfg      *config.Config
	st       *styles
	save     SaveFunc       // nil: changes are only kept in memory
	items    []launcherItem // groups first, then projects, each sorted
	selected map[string]bool
	missing  map[string]bool   // projects whose dir does not exist
	cursor   int               // index into items
	cardCol  int               // column of the card the cursor left the cards from, downwards
	top      int               // first row shown, see rows
	notice   string            // shown instead of the help line until the next key
	form     *editForm         // open project or group form, or nil
	confirm  *confirmation     // open delete confirmation, or nil
	picker   *picker           // open branch or theme picker, or nil
	ask      *question         // open question in the help line (pull?), or nil
	output   []string          // output of a failed git action, shown until the next key
	skipped  bool              // output is about skipped repositories only (P), not failures
	jump     bool              // jump mode (/): every item shows its letter, see jumpLabel
	lastArgs map[string]string // extra arguments typed last, by project (R)

	git       *gitTracker // nil: git is not installed
	gitStale  bool        // the config or a repository changed; refresh the git state
	gitAction string      // git action running in the background, e.g. "git switch"
	cleaning  bool        // leftover processes are being stopped

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
	case fetchedAllMsg:
		if msg.owner == m {
			m.fetchedAll(msg)
		}
		return m, nil
	case pulledAllMsg:
		if msg.owner == m {
			m.pulledAll(msg)
		}
		return m, nil
	case leftoversMsg:
		if msg.owner == m {
			m.notice, m.cleaning = msg.report, false
		}
		return m, nil
	}
	switch {
	case m.form != nil && m.picker != nil: // the directory browser
		switch msg.(type) {
		case tea.KeyPressMsg, tea.PasteMsg:
			return m, m.updatePicker(msg)
		}
		return m, m.updateForm(msg)
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
	case m.jump:
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			return m, m.updateJump(msg.String())
		}
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		m.notice = ""
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "down", "left", "right":
			m.move(msg.String())
		case "home":
			m.cursor = 0
		case "end":
			m.cursor = max(len(m.items)-1, 0)
		case "space":
			m.toggle()
		case "enter":
			return m, m.startSelection()
		case "R", "shift+enter":
			return m, m.openArgsForm()
		case "/":
			m.jump = len(m.items) > 0
		case "n":
			return m, m.openForm(newProjectForm(m.st, m.cfg, "", m.gitReader()))
		case "e":
			if it, ok := m.current(); ok && it.group {
				return m, m.openForm(newGroupForm(m.st, m.cfg, it.name, nil))
			} else if ok {
				return m, m.openForm(newProjectForm(m.st, m.cfg, it.name, m.gitReader()))
			}
		case "g":
			if it, ok := m.current(); ok && it.group {
				return m, m.openForm(newGroupForm(m.st, m.cfg, it.name, nil))
			}
			return m, m.openForm(newGroupForm(m.st, m.cfg, "", slices.Sorted(maps.Keys(m.selected))))
		case "d":
			if it, ok := m.current(); ok {
				m.confirm = newDeleteConfirmation(m.cfg, it)
			}
		case "b":
			return m, m.openBranches()
		case "F":
			return m, m.fetchAllRepos()
		case "P":
			m.confirmPullAll()
		case "p":
			return m, m.openPull()
		case "t":
			return m, m.openThemes()
		case "c":
			if it, ok := m.current(); ok && !it.group {
				var cmd tea.Cmd
				m.notice, cmd = copyCommand(m.cfg, it.name, "")
				return m, cmd
			}
		}
	}
	return m, nil
}

// startSelection starts the selection, or the item under the cursor when
// nothing is selected.
func (m *launcherModel) startSelection() tea.Cmd {
	return m.start(m.toStart(), nil)
}

// toStart returns what enter starts: the selection, or the item under the
// cursor when nothing is selected.
func (m *launcherModel) toStart() []string {
	names := m.selection()
	if len(names) == 0 && m.cursor < len(m.items) {
		names = []string{m.items[m.cursor].name}
	}
	return names
}

// openArgsForm opens the form asking for extra arguments for what enter
// would start, pre-filled with the ones typed last.
func (m *launcherModel) openArgsForm() tea.Cmd {
	names := m.toStart()
	if len(names) == 0 {
		return nil
	}
	f, err := newArgsForm(m.st, m.cfg, names, m.lastArgs, func(args map[string]string) tea.Cmd {
		if m.lastArgs == nil {
			m.lastArgs = map[string]string{}
		}
		for p, a := range args {
			if a == "" {
				delete(m.lastArgs, p)
			} else {
				m.lastArgs[p] = a
			}
		}
		return m.start(names, args)
	})
	if err != nil {
		m.notice = oneLine(err.Error())
		return nil
	}
	return m.openForm(f)
}

// start asks the app to start names with extra arguments by project (nil
// for none), unless a git action or the cleanup of leftover processes is
// still running; then a notice says so.
func (m *launcherModel) start(names []string, args map[string]string) tea.Cmd {
	switch {
	case len(names) == 0:
		return nil
	case m.gitAction != "":
		m.notice = m.gitAction + " is still running"
		return nil
	case m.cleaning:
		m.notice = "the leftover processes are still being stopped"
		return nil
	}
	return func() tea.Msg { return startMsg{names, args} }
}

// updateJump handles a key in jump mode: a group's letter starts the group
// (the selection is left out), a project's letter selects or unselects the
// project and moves the cursor to it, staying in jump mode. enter starts
// what it starts outside jump mode.
func (m *launcherModel) updateJump(key string) tea.Cmd {
	m.notice = ""
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "esc", "/":
		m.jump = false
		return nil
	case "enter":
		m.jump = false
		return m.startSelection()
	}
	i := jumpIndex(key)
	if i < 0 || i >= len(m.items) {
		return nil
	}
	if it := m.items[i]; it.group {
		m.jump = false
		return m.start([]string{it.name}, nil)
	}
	m.cursor = i
	m.toggle()
	return nil
}

// jumpLabel returns the letter of the item at index i in jump mode: a–z,
// then A–Z, and "" for the items after those.
func jumpLabel(i int) string {
	switch {
	case i < 0:
		return ""
	case i < 26:
		return string(rune('a' + i))
	case i < 52:
		return string(rune('A' + i - 26))
	}
	return ""
}

// jumpIndex returns the index of the item whose letter is key, or -1.
func jumpIndex(key string) int {
	if len(key) != 1 {
		return -1
	}
	switch c := key[0]; {
	case c >= 'a' && c <= 'z':
		return int(c - 'a')
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 26
	}
	return -1
}

// jumpLabelAt returns the letter shown for the item at index i: its
// jumpLabel in jump mode, "" otherwise.
func (m *launcherModel) jumpLabelAt(i int) string {
	if !m.jump {
		return ""
	}
	return jumpLabel(i)
}

// openThemes opens the theme picker, on the current theme. The launcher is
// drawn in the theme under the cursor until one is picked or esc restores
// the saved one.
func (m *launcherModel) openThemes() tea.Cmd {
	current := m.cfg.Settings.Theme()
	items := make([]pickerItem, len(config.ThemeNames))
	for i, name := range config.ThemeNames {
		t := themeNamed(name)
		items[i] = pickerItem{name: name, label: name, info: t.about}
		if name == current {
			items[i].pill = "current"
		}
		if m.st.tints() {
			items[i].swatch = m.st.swatch(t)
		}
	}
	p := &picker{
		title: "theme", action: "use", mode: "theme", noun: "theme", items: items,
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
	return m.form != nil || m.confirm != nil || m.picker != nil || m.ask != nil || m.output != nil || m.jump
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
	label := m.st.bold.Foreground(m.st.color(m.cfg.ColorOf(msg.project))).Render(msg.project)
	context := branchContext(m.st, label, m.cfg.Projects[msg.project].Dir, m.git, msg.project)
	var cmd tea.Cmd
	m.picker, cmd = branchPicker(m.st, msg, msg.project, context, m.sharing(msg.project, msg.repo.Root), func(b gitinfo.Branch) tea.Cmd {
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
		return m.picker.update(msg) // a paste
	}
	m.notice = ""
	if m.picker.keys != nil {
		if done, cmd := m.picker.keys(key.String()); done {
			return cmd
		}
	}
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
		m.output, m.skipped = switchLines(msg), false
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
		m.output, m.skipped = pullLines(msg), false
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
	return f.init()
}

// gitReader returns what reads git state, or nil if git isn't installed.
func (m *launcherModel) gitReader() GitReader {
	if m.git == nil {
		return nil
	}
	return m.git.reader
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
		case "ctrl+o":
			if fd := m.form.form.focused(); fd != nil && fd.key == "dir" {
				return m.browseDirs(fd)
			}
		}
	}
	cmd := m.form.update(msg)
	if f := m.form.form; f.done {
		if !f.saved {
			m.form = nil
			return cmd
		}
		if submit := m.form.submit; submit != nil {
			m.form = nil
			return tea.Batch(cmd, submit())
		}
		m.submitForm()
	}
	return cmd
}

// browseDirs opens the directory browser over the project form, starting
// at the directory in fd. ctrl+o in it puts the directory shown into fd.
func (m *launcherModel) browseDirs(fd *formField) tea.Cmd {
	var cmd tea.Cmd
	m.picker, cmd = newDirBrowser(m.st, browseStart(*fd.str), func(dir string) tea.Cmd {
		m.picker = nil
		fd.setValue(config.DisplayPath(dir))
		fd.touched = true
		m.form.form.validate()
		return m.form.handle(nil) // checks the directory
	})
	return cmd
}

// submitForm saves a form closed with Save, or keeps it open with the error
// shown.
func (m *launcherModel) submitForm() {
	f := m.form
	c, focus, err := f.result()
	if err == nil {
		_, err = c.Validate()
	}
	if err == nil && m.save != nil {
		err = m.save(c)
	}
	if err != nil {
		m.notice = oneLine(err.Error())
		f.form.done = false
		return
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

// count returns "1 project", "2 projects", "2 branches".
func count(n int, noun string) string {
	switch {
	case n == 1:
		return "1 " + noun
	case strings.HasSuffix(noun, "ch"), strings.HasSuffix(noun, "s"):
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
