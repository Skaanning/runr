package tui

import (
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"
)

// The two screens git actions run in (see gitHost), and the keys that start
// them there.

// launcherGit is the launcher as a gitHost: projects are shown by name,
// every registered project counts, and git's output of a failure replaces
// the list until the next key.
type launcherGit struct{ m *launcherModel }

func (h launcherGit) tracker() *gitTracker  { return h.m.git }
func (h launcherGit) label(p string) string { return p }
func (h launcherGit) styledLabel(p string) string {
	return h.m.st.bold.Foreground(h.m.st.color(h.m.cfg.ColorOf(p))).Render(p)
}
func (h launcherGit) sharing(project, root string) []string {
	return h.m.git.inRepo(project, root, slices.Sorted(maps.Keys(h.m.cfg.Projects)))
}
func (h launcherGit) busy() bool                  { return h.m.busy() }
func (h launcherGit) setNotice(n string)          { h.m.notice = n }
func (h launcherGit) openPicker(p *picker)        { h.m.picker = p }
func (h launcherGit) askQuestion(q *question)     { h.m.ask = q }
func (h launcherGit) note(string, ...string) bool { return false } // no logs here
func (h launcherGit) showOutput(lines []string, skippedOnly bool) {
	h.m.output, h.m.skipped = lines, skippedOnly
}
func (h launcherGit) refresh() tea.Cmd { h.m.gitStale = true; return nil } // see launcherModel.Update

// runningGit is the running view as a gitHost: projects are shown by their
// labels, the projects in the session count, and git's output goes to their
// logs.
type runningGit struct{ m *runningModel }

func (h runningGit) tracker() *gitTracker  { return h.m.git }
func (h runningGit) label(p string) string { return h.m.labelOf(p) }
func (h runningGit) styledLabel(p string) string {
	return h.m.styleOf(p).Bold(true).Render(h.m.labelOf(p))
}
func (h runningGit) sharing(project, root string) []string {
	return h.m.git.inRepo(project, root, h.m.sessionNames())
}
func (h runningGit) busy() bool {
	m := h.m
	return m.stopping || m.picker != nil || m.confirm != nil || m.editing || m.argsFor != "" || m.attached != ""
}
func (h runningGit) setNotice(n string)      { h.m.notice = n }
func (h runningGit) openPicker(p *picker)    { h.m.picker = p }
func (h runningGit) askQuestion(q *question) { h.m.confirm = q }

// note adds the lines to the project's log; projects not started have none.
func (h runningGit) note(project string, lines ...string) bool {
	_ = h.m.sess.Note(project, lines...)
	return h.m.byName[project] != nil
}
func (h runningGit) showOutput([]string, bool) {} // it is in the logs
func (h runningGit) refresh() tea.Cmd          { return h.m.git.refresh(h.m.gitDirs()) }

// sessionNames returns the projects in the session, in sidebar order.
func (m *runningModel) sessionNames() []string {
	names := make([]string, len(m.entries))
	for i, e := range m.entries {
		names[i] = e.info.Name
	}
	return names
}

// gitProject returns the project under the cursor for a git action (what,
// e.g. "pull"); on a group, a notice says to move to a project.
func (m *launcherModel) gitProject(what string) (string, bool) {
	it, ok := m.current()
	if ok && it.group {
		m.notice = "move to a project to " + what
		return "", false
	}
	return it.name, ok
}

// openBranches opens the branch picker for the project under the cursor (b).
func (m *launcherModel) openBranches() tea.Cmd {
	if name, ok := m.gitProject("switch its branch"); ok {
		return m.gitOps.openBranches(name)
	}
	return nil
}

// openPull asks whether to pull the project under the cursor (p).
func (m *launcherModel) openPull() tea.Cmd {
	if name, ok := m.gitProject("pull"); ok {
		return m.gitOps.openPull(name)
	}
	return nil
}

// fetchAllRepos fetches the repository of every registered project (F).
func (m *launcherModel) fetchAllRepos() tea.Cmd {
	return m.gitOps.fetchAllRepos(slices.Sorted(maps.Keys(m.cfg.Projects)), "no project is in a git repository")
}

// confirmPullAll asks whether to pull the repositories of the selection, or
// of the group or project under the cursor (P).
func (m *launcherModel) confirmPullAll() {
	names := m.selection()
	what := "the selection"
	if len(names) == 0 {
		it, ok := m.current()
		if !ok {
			return
		}
		names, what = []string{it.name}, it.name
	}
	var projects []string
	for _, n := range names {
		if members, ok := m.cfg.Groups[n]; ok {
			projects = append(projects, members...)
		} else {
			projects = append(projects, n)
		}
	}
	m.gitOps.confirmPullAll(projects, "nothing in "+what+" is in a git repository")
}

// openBranches opens the branch picker for the selected project (b).
func (m *runningModel) openBranches() tea.Cmd {
	if name := m.selectedName(); name != "" && !m.stopping {
		return m.gitOps.openBranches(name)
	}
	return nil
}

// openPull asks whether to pull the selected project (p).
func (m *runningModel) openPull() tea.Cmd {
	if name := m.selectedName(); name != "" && !m.stopping {
		return m.gitOps.openPull(name)
	}
	return nil
}

// fetchAllRepos fetches every repository of the session (F).
func (m *runningModel) fetchAllRepos() tea.Cmd {
	if m.stopping {
		return nil
	}
	return m.gitOps.fetchAllRepos(m.sessionNames(), "no project in this session is in a git repository")
}

// confirmPullAll asks whether to pull every repository of the session (P).
func (m *runningModel) confirmPullAll() {
	if !m.stopping {
		m.gitOps.confirmPullAll(m.sessionNames(), "no project in this session is in a git repository")
	}
}
