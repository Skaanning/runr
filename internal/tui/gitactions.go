package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"runr/internal/gitinfo"
)

// Git actions run on the selected project's repository, in the background.
// Before anything is changed, the repository's current state is checked
// (gitinfo.Status.CheckSafe). git's output goes to the selected project's
// log; other projects in the session that share the repository get a line
// saying what changed.

type (
	branchesMsg struct { // branches read for the picker
		project  string
		repo     gitinfo.Repo
		branches []gitinfo.Branch
		err      error // reading failed, or the preflight refused
	}
	switchedMsg struct { // a branch switch finished
		project string
		repo    gitinfo.Repo
		branch  gitinfo.Branch
		ran     bool // git switch ran; false if the preflight refused
		output  string
		err     error
	}
	pullCheckMsg struct { // the repository was checked before asking to pull
		project string
		repo    gitinfo.Repo
		status  gitinfo.Status
		err     error // reading failed, or the preflight refused
	}
	pulledMsg struct { // a pull finished
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
}

// gitTarget returns the repository of a project for a git action. If there
// is none, or another action is running, it sets a notice saying why.
func (m *runningModel) gitTarget(name string) (gitinfo.Repo, bool) {
	if m.git == nil {
		m.notice = "git is not installed"
		return gitinfo.Repo{}, false
	}
	g, ok := m.git.get(name)
	switch {
	case !ok:
		m.notice = m.entry(name).label + " is not in a git repository"
	case g.err != nil:
		m.notice = g.err.Error()
	case m.gitAction != "":
		m.notice = m.gitAction + " is still running"
	default:
		return m.git.repos.ByRoot[g.root], true
	}
	return gitinfo.Repo{}, false
}

// sharing returns the other projects in the session that are in the
// repository at root.
func (m *runningModel) sharing(project, root string) []string {
	var names []string
	for _, e := range m.entries {
		if g, ok := m.git.get(e.info.Name); ok && e.info.Name != project && g.root == root {
			names = append(names, e.info.Name)
		}
	}
	return names
}

func (m *runningModel) labels(names []string) string {
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = m.entry(n).label
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
	e := m.selectedEntry()
	if e == nil || m.stopping {
		return nil
	}
	repo, ok := m.gitTarget(e.info.Name)
	if !ok {
		return nil
	}
	reader, name := m.git.reader, e.info.Name
	return func() tea.Msg {
		msg := branchesMsg{project: name, repo: repo}
		if msg.err = preflight(reader, repo); msg.err == nil {
			msg.branches, msg.err = reader.Branches(context.Background(), repo)
		}
		return msg
	}
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
	byName := map[string]gitinfo.Branch{}
	current := "a detached HEAD"
	var items []pickerItem
	for _, b := range msg.branches {
		byName[b.Name] = b
		var info []string
		switch {
		case b.Current:
			current = b.Name
			info = append(info, "current")
		case b.Remote:
			info = append(info, "new branch from "+b.Name)
		}
		if b.Date != "" {
			info = append(info, b.Date)
		}
		items = append(items, pickerItem{name: b.Name, label: b.LocalName(), info: strings.Join(info, " · ")})
	}
	note := fmt.Sprintf("%s is on %s", m.entry(msg.project).label, current)
	if others := m.sharing(msg.project, msg.repo.Root); len(others) == 1 {
		note += " · " + m.labels(others) + " shares the repository and switches too"
	} else if len(others) > 1 {
		note += " · " + m.labels(others) + " share the repository and switch too"
	}
	var cmd tea.Cmd
	m.picker, cmd = newPicker(&picker{
		action: "switch", note: note, empty: "no branches", items: items,
		pick: func(it pickerItem) tea.Cmd { return m.switchBranch(msg.project, msg.repo, byName[it.name]) },
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
	m.notice = fmt.Sprintf("switching %s to %s…", m.entry(project).label, b.LocalName())
	reader := m.git.reader
	return func() tea.Msg {
		msg := switchedMsg{project: project, repo: repo, branch: b}
		if msg.err = preflight(reader, repo); msg.err != nil {
			return msg
		}
		msg.ran = true
		msg.output, msg.err = reader.Switch(context.Background(), repo, b)
		return msg
	}
}

// switched reports the outcome of a branch switch in the help line and in
// the logs of the projects in the repository, then refreshes the git state.
func (m *runningModel) switched(msg switchedMsg) tea.Cmd {
	m.gitAction = ""
	if !msg.ran {
		m.notice = "can't switch branches: " + msg.err.Error()
		return nil
	}
	label, branch := m.entry(msg.project).label, msg.branch.LocalName()
	lines := append([]string{"$ git " + strings.Join(gitinfo.SwitchArgs(msg.branch), " ")}, outputLines(msg.output)...)
	if msg.err != nil {
		_ = m.sess.Note(msg.project, append(lines, "git switch failed")...)
		m.notice = msg.err.Error() + " (output in the log)"
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
	e := m.selectedEntry()
	if e == nil || m.stopping {
		return nil
	}
	repo, ok := m.gitTarget(e.info.Name)
	if !ok {
		return nil
	}
	reader, name := m.git.reader, e.info.Name
	return func() tea.Msg {
		msg := pullCheckMsg{project: name, repo: repo}
		msg.status, msg.err = pullPreflight(reader, repo)
		return msg
	}
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
	prompt := fmt.Sprintf("pull %s into %s (%s)?", msg.status.Upstream, m.entry(msg.project).label, msg.status.Branch)
	if others := m.sharing(msg.project, msg.repo.Root); len(others) > 0 {
		prompt += " also updates " + m.labels(others) + " (same repository)"
	}
	m.notice = ""
	m.confirm = &question{
		prompt: prompt, action: "pull",
		yes: func() tea.Cmd { return m.pull(msg.project, msg.repo) },
	}
}

// pull checks the repository again and pulls in the background.
func (m *runningModel) pull(project string, repo gitinfo.Repo) tea.Cmd {
	if m.gitAction != "" {
		m.notice = m.gitAction + " is still running"
		return nil
	}
	m.gitAction = "git pull"
	m.notice = fmt.Sprintf("pulling %s…", m.entry(project).label)
	reader := m.git.reader
	return func() tea.Msg {
		msg := pulledMsg{project: project, repo: repo}
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
	label := m.entry(msg.project).label
	lines := append([]string{"$ git " + strings.Join(gitinfo.PullArgs(), " ")}, outputLines(msg.output)...)
	if msg.err != nil {
		_ = m.sess.Note(msg.project, append(lines, "git pull failed")...)
		m.notice = msg.err.Error() + " (output in the log)"
		return m.git.refresh(m.gitDirs())
	}
	before, after := msg.before.ShortCommit(), msg.after.ShortCommit()
	switch {
	case after == before:
		_ = m.sess.Note(msg.project, append(lines, "already up to date")...)
		m.notice = fmt.Sprintf("%s is already up to date", label)
	case after == "": // the state couldn't be read afterwards
		_ = m.sess.Note(msg.project, append(lines, "pulled "+msg.before.Upstream)...)
		m.notice = fmt.Sprintf("pulled %s into %s", msg.before.Upstream, label)
	default:
		change := fmt.Sprintf("%s %s → %s", msg.before.Branch, before, after)
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
