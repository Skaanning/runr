package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/gitinfo"
)

// Git actions run on the selected project's repository, in the background.
// Before anything is changed, the repository's current state is checked
// (gitinfo.Status.CheckSafe). In the running view, git's output goes to the
// selected project's log, and other projects in the session that share the
// repository get a line saying what changed. The launcher can switch
// branches and pull too (launcherModel.openBranches, launcherModel.openPull).

type (
	branchesMsg struct { // branches read for the picker
		owner    any // the model that asked
		project  string
		repo     gitinfo.Repo
		branches []gitinfo.Branch
		err      error // reading failed, or the preflight refused
	}
	switchedMsg struct { // a branch switch finished
		owner   any
		project string
		repo    gitinfo.Repo
		branch  gitinfo.Branch
		ran     bool // git switch ran; false if the preflight refused
		output  string
		err     error
	}
	pullCheckMsg struct { // the repository was checked before asking to pull
		owner   any
		project string
		repo    gitinfo.Repo
		status  gitinfo.Status
		err     error // reading failed, or the preflight refused
	}
	pulledMsg struct { // a pull finished
		owner         any
		project       string
		repo          gitinfo.Repo
		ran           bool // git pull ran; false if the preflight refused
		before, after gitinfo.Status
		output        string
		err           error
	}
)

// question is a yes/no question shown in the help line.
type question struct {
	prompt string
	action string // what enter does, e.g. "pull"
	yes    func() tea.Cmd
	no     func() tea.Cmd // what esc does, if more than closing the question
	cancel string         // the hint for esc; "cancel" if empty
}

// target returns the repository of a project (shown as label) for a git
// action. If there is none, or the action busy is still running, it returns
// a notice saying why instead.
func (t *gitTracker) target(name, label, busy string) (gitinfo.Repo, string) {
	if t == nil {
		return gitinfo.Repo{}, "git is not installed"
	}
	g, ok := t.get(name)
	switch {
	case !ok:
		return gitinfo.Repo{}, label + " is not in a git repository"
	case g.err != nil:
		return gitinfo.Repo{}, g.err.Error()
	case busy != "":
		return gitinfo.Repo{}, busy + " is still running"
	}
	return t.repos.ByRoot[g.root], ""
}

// inRepo returns those of names, other than project, whose directories are
// in the repository at root.
func (t *gitTracker) inRepo(project, root string, names []string) []string {
	var in []string
	for _, name := range names {
		if g, ok := t.get(name); ok && name != project && g.root == root {
			in = append(in, name)
		}
	}
	return in
}

// gitTarget returns the repository of a project for a git action. If there
// is none, or another action is running, it sets a notice saying why.
func (m *runningModel) gitTarget(name string) (gitinfo.Repo, bool) {
	repo, notice := m.git.target(name, m.labelOf(name), m.gitAction)
	m.notice = notice
	return repo, notice == ""
}

// sharing returns the other projects in the session that are in the
// repository at root.
func (m *runningModel) sharing(project, root string) []string {
	names := make([]string, len(m.entries))
	for i, e := range m.entries {
		names[i] = e.info.Name
	}
	return m.git.inRepo(project, root, names)
}

// outputNote points to the log for a git action's output, if the project
// has one (projects not started don't).
func (m *runningModel) outputNote(project string) string {
	if m.byName[project] == nil {
		return ""
	}
	return " (output in the log)"
}

func (m *runningModel) labels(names []string) string {
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = m.labelOf(n)
	}
	return strings.Join(labels, ", ")
}

// preflight reads the current state of repo and returns why runr won't
// switch branches there, or nil.
func preflight(reader GitReader, repo gitinfo.Repo) error {
	s, err := reader.Status(context.Background(), repo)
	if err != nil {
		return err
	}
	return s.CheckSafe()
}

// pullPreflight reads the current state of repo and returns it, with why
// runr won't pull there, or nil.
func pullPreflight(reader GitReader, repo gitinfo.Repo) (gitinfo.Status, error) {
	s, err := reader.Status(context.Background(), repo)
	if err != nil {
		return s, err
	}
	return s, s.CheckPull()
}

// openBranches checks the selected project's repository and reads its
// branches in the background; the picker opens when they arrive.
func (m *runningModel) openBranches() tea.Cmd {
	name := m.selectedName()
	if name == "" || m.stopping {
		return nil
	}
	repo, ok := m.gitTarget(name)
	if !ok {
		return nil
	}
	return readBranches(m, m.git.reader, name, repo)
}

// readBranches checks repo and reads its branches in the background, for
// the picker.
func readBranches(owner any, reader GitReader, project string, repo gitinfo.Repo) tea.Cmd {
	return func() tea.Msg {
		msg := branchesMsg{owner: owner, project: project, repo: repo}
		if msg.err = preflight(reader, repo); msg.err == nil {
			msg.branches, msg.err = reader.Branches(context.Background(), repo)
		}
		return msg
	}
}

// branchPicker returns the picker for the branches in msg, local and remote
// ones in their own sections. context is the line above the filter (see
// branchContext); others are the other projects, as shown, that switch with
// the project (shown as label).
func branchPicker(st *styles, msg branchesMsg, label, context string, others []string, pick func(gitinfo.Branch) tea.Cmd) (*picker, tea.Cmd) {
	byName := map[string]gitinfo.Branch{}
	var items []pickerItem
	for _, b := range msg.branches {
		byName[b.Name] = b
		it := pickerItem{name: b.Name, label: b.LocalName(), section: "Local", info: b.Subject, right: b.Date}
		if b.Current {
			it.mark, it.pill = st.accent.Render("●"), "current"
		}
		var track []string
		if b.Ahead > 0 {
			track = append(track, fmt.Sprintf("↑%d", b.Ahead))
		}
		if b.Behind > 0 {
			track = append(track, fmt.Sprintf("↓%d", b.Behind))
		}
		it.track = strings.Join(track, " ")
		if b.Remote {
			remote, _, _ := strings.Cut(b.Name, "/")
			it.section, it.right = "Remote", "from "+remote
			if b.Date != "" {
				it.right += " · " + b.Date
			}
		}
		items = append(items, it)
	}
	p := &picker{
		title: "switch branch · " + label, action: "switch", context: context, empty: "no branches",
		noun: "branch", items: items,
		pick: func(it pickerItem) tea.Cmd { return pick(byName[it.name]) },
		footer: func(it pickerItem) string {
			b := byName[it.name]
			if b.Current {
				return st.faint.Render("already on " + b.Name)
			}
			return st.accent.Render("$ ") + st.faint.Render("git "+strings.Join(gitinfo.SwitchArgs(b), " "))
		},
	}
	if len(others) > 0 {
		p.chip = "also switches: " + strings.Join(others, ", ")
	}
	return newPicker(st, p)
}

// branchContext renders the project (label, styled) a branch picker is for,
// its directory and its git label: "api · ~/code/api · main ↑1".
func branchContext(st *styles, label, dir string, git *gitTracker, project string) string {
	line := label
	if dir != "" {
		line += st.faint.Render(" · " + config.DisplayPath(dir))
	}
	if g, ok := git.get(project); ok {
		line += st.faint.Render(" · ") + gitLabel(st, g, gitWidth(g))
	}
	return line
}

// showBranches opens the branch picker.
func (m *runningModel) showBranches(msg branchesMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = "can't switch branches: " + msg.err.Error()
		return nil
	}
	if m.stopping || m.picker != nil || m.editing {
		return nil // something else took over the screen meanwhile
	}
	others := m.sharing(msg.project, msg.repo.Root)
	for i, name := range others {
		others[i] = m.labelOf(name)
	}
	label := m.styleOf(msg.project).Bold(true).Render(m.labelOf(msg.project))
	context := branchContext(m.st, label, m.cfg.Projects[msg.project].Dir, m.git, msg.project)
	var cmd tea.Cmd
	m.picker, cmd = branchPicker(m.st, msg, m.labelOf(msg.project), context, others, func(b gitinfo.Branch) tea.Cmd {
		return m.switchBranch(msg.project, msg.repo, b)
	})
	return cmd
}

// switchBranch checks the repository again and switches it to b in the
// background.
func (m *runningModel) switchBranch(project string, repo gitinfo.Repo, b gitinfo.Branch) tea.Cmd {
	if b.Current {
		m.notice = "already on " + b.Name
		return nil
	}
	if m.gitAction != "" {
		m.notice = m.gitAction + " is still running"
		return nil
	}
	m.gitAction = "git switch"
	m.notice = fmt.Sprintf("switching %s to %s…", m.labelOf(project), b.LocalName())
	return runSwitch(m, m.git.reader, project, repo, b)
}

// runSwitch checks repo again and switches it to b in the background.
func runSwitch(owner any, reader GitReader, project string, repo gitinfo.Repo, b gitinfo.Branch) tea.Cmd {
	return func() tea.Msg {
		msg := switchedMsg{owner: owner, project: project, repo: repo, branch: b}
		if msg.err = preflight(reader, repo); msg.err != nil {
			return msg
		}
		msg.ran = true
		msg.output, msg.err = reader.Switch(context.Background(), repo, b)
		return msg
	}
}

// switchLines returns what a branch switch printed, after the command.
func switchLines(msg switchedMsg) []string {
	return append([]string{"$ git " + strings.Join(gitinfo.SwitchArgs(msg.branch), " ")}, outputLines(msg.output)...)
}

// switched reports the outcome of a branch switch in the help line and in
// the logs of the projects in the repository, then refreshes the git state.
func (m *runningModel) switched(msg switchedMsg) tea.Cmd {
	m.gitAction = ""
	if !msg.ran {
		m.notice = "can't switch branches: " + msg.err.Error()
		return nil
	}
	label, branch := m.labelOf(msg.project), msg.branch.LocalName()
	lines := switchLines(msg)
	if msg.err != nil {
		_ = m.sess.Note(msg.project, append(lines, "git switch failed")...)
		m.notice = msg.err.Error() + m.outputNote(msg.project)
		return m.git.refresh(m.gitDirs())
	}
	_ = m.sess.Note(msg.project, append(lines, "switched to "+branch)...)
	m.notice = fmt.Sprintf("switched %s to %s", label, branch)
	if others := m.sharing(msg.project, msg.repo.Root); len(others) > 0 {
		m.notice += " (also " + m.labels(others) + ")"
		for _, name := range others {
			_ = m.sess.Note(name, fmt.Sprintf("switched to %s (git switch run for %s, same repository)", branch, label))
		}
	}
	return m.git.refresh(m.gitDirs())
}

// openPull checks the selected project's repository in the background; the
// confirmation opens when it is done.
func (m *runningModel) openPull() tea.Cmd {
	name := m.selectedName()
	if name == "" || m.stopping {
		return nil
	}
	repo, ok := m.gitTarget(name)
	if !ok {
		return nil
	}
	return checkPull(m, m.git.reader, name, repo)
}

// checkPull checks repo in the background before asking whether to pull.
func checkPull(owner any, reader GitReader, project string, repo gitinfo.Repo) tea.Cmd {
	return func() tea.Msg {
		msg := pullCheckMsg{owner: owner, project: project, repo: repo}
		msg.status, msg.err = pullPreflight(reader, repo)
		return msg
	}
}

// pullQuestion asks whether to pull into the project shown as label, naming
// the other projects (others, as shown) that the pull updates too.
func pullQuestion(msg pullCheckMsg, label string, others []string, yes func() tea.Cmd) *question {
	prompt := fmt.Sprintf("pull %s into %s (%s)?", msg.status.Upstream, label, msg.status.Branch)
	if len(others) > 0 {
		prompt += " also updates " + strings.Join(others, ", ") + " (same repository)"
	}
	return &question{prompt: prompt, action: "pull", yes: yes}
}

// questionLine renders an open question as the help line. The hints stay
// visible; the question is cut short when narrow.
func questionLine(st *styles, q *question, width int) string {
	cancel := q.cancel
	if cancel == "" {
		cancel = "cancel"
	}
	hints := st.hintGap + st.hintList([][2]string{{"enter", q.action}, {"esc", cancel}})
	w := max(width-ansi.StringWidth(hints)-1, 1)
	return ansi.Truncate(" "+st.bold.Render(ansi.Truncate(q.prompt, w, "…"))+hints, width, "…")
}

// answer handles a key while q is open: enter or y answers yes; esc, n or q
// cancel. It reports whether q is done, with yes's command.
func (q *question) answer(key string) (done bool, cmd tea.Cmd) {
	switch key {
	case "esc", "n", "q":
		if q.no != nil {
			return true, q.no()
		}
		return true, nil
	case "enter", "y":
		return true, q.yes()
	}
	return false, nil
}

// confirmPull asks whether to pull, naming the other projects in the session
// that the pull updates too.
func (m *runningModel) confirmPull(msg pullCheckMsg) {
	if msg.err != nil {
		m.notice = "can't pull: " + msg.err.Error()
		return
	}
	if m.stopping || m.picker != nil || m.editing {
		return // something else took over the screen meanwhile
	}
	others := m.sharing(msg.project, msg.repo.Root)
	for i, name := range others {
		others[i] = m.labelOf(name)
	}
	m.notice = ""
	m.confirm = pullQuestion(msg, m.labelOf(msg.project), others, func() tea.Cmd { return m.pull(msg.project, msg.repo) })
}

// pull checks the repository again and pulls in the background.
func (m *runningModel) pull(project string, repo gitinfo.Repo) tea.Cmd {
	if m.gitAction != "" {
		m.notice = m.gitAction + " is still running"
		return nil
	}
	m.gitAction = "git pull"
	m.notice = fmt.Sprintf("pulling %s…", m.labelOf(project))
	return runPull(m, m.git.reader, project, repo)
}

// runPull checks repo again and pulls in the background.
func runPull(owner any, reader GitReader, project string, repo gitinfo.Repo) tea.Cmd {
	return func() tea.Msg {
		msg := pulledMsg{owner: owner, project: project, repo: repo}
		if msg.before, msg.err = pullPreflight(reader, repo); msg.err != nil {
			return msg
		}
		msg.ran = true
		msg.output, msg.err = reader.Pull(context.Background(), repo)
		msg.after, _ = reader.Status(context.Background(), repo) // only used to report what changed
		return msg
	}
}

// pulled reports the outcome of a pull in the help line and in the logs of
// the projects in the repository, then refreshes the git state.
func (m *runningModel) pulled(msg pulledMsg) tea.Cmd {
	m.gitAction = ""
	if !msg.ran {
		m.notice = "can't pull: " + msg.err.Error()
		return nil
	}
	label := m.labelOf(msg.project)
	lines := pullLines(msg)
	if msg.err != nil {
		_ = m.sess.Note(msg.project, append(lines, "git pull failed")...)
		m.notice = msg.err.Error() + m.outputNote(msg.project)
		return m.git.refresh(m.gitDirs())
	}
	change := pullChange(msg)
	switch {
	case change == "":
		_ = m.sess.Note(msg.project, append(lines, "already up to date")...)
		m.notice = fmt.Sprintf("%s is already up to date", label)
	case msg.after.Commit == "": // the state couldn't be read afterwards
		_ = m.sess.Note(msg.project, append(lines, "pulled "+change)...)
		m.notice = fmt.Sprintf("pulled %s into %s", change, label)
	default:
		_ = m.sess.Note(msg.project, append(lines, "pulled "+change)...)
		m.notice = fmt.Sprintf("pulled %s: %s", label, change)
		if others := m.sharing(msg.project, msg.repo.Root); len(others) > 0 {
			m.notice += " (also " + m.labels(others) + ")"
			for _, name := range others {
				_ = m.sess.Note(name, fmt.Sprintf("pulled %s (git pull run for %s, same repository)", change, label))
			}
		}
	}
	return m.git.refresh(m.gitDirs())
}

// pullLines returns what a pull printed, after the command.
func pullLines(msg pulledMsg) []string {
	return append([]string{"$ git " + strings.Join(gitinfo.PullArgs(), " ")}, outputLines(msg.output)...)
}

// pullChange describes what a successful pull changed, e.g.
// "main 1e88f8a → 9d7ae2a", or "" if HEAD didn't move. If the state couldn't
// be read afterwards, it names the upstream that was pulled.
func pullChange(msg pulledMsg) string {
	before, after := msg.before.ShortCommit(), msg.after.ShortCommit()
	switch {
	case after == before:
		return ""
	case after == "":
		return msg.before.Upstream
	}
	return fmt.Sprintf("%s %s → %s", msg.before.Branch, before, after)
}

// outputLines splits command output into lines, keeping only what a terminal
// would show of lines rewritten with \r (progress counters).
func outputLines(out string) []string {
	var lines []string
	for line := range strings.Lines(strings.TrimRight(out, "\n")) {
		line = strings.TrimRight(line, "\r\n")
		if i := strings.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:]
		}
		lines = append(lines, line)
	}
	return lines
}
