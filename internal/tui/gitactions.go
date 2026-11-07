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

// Git actions run on a project's repository, in the background. Before
// anything is changed, the repository's current state is checked
// (gitinfo.Status.CheckSafe). The launcher and the running view share the
// actions through gitActions; what differs between them is behind gitHost.
// In the running view, git's output goes to the projects' logs, and other
// projects in the session that share the repository get a line saying what
// changed; in the launcher, the output of a failure replaces the list until
// the next key.

type (
	branchesMsg struct { // branches read for the picker
		owner    *gitActions // the screen's actions that asked
		project  string
		repo     gitinfo.Repo
		branches []gitinfo.Branch
		err      error // reading failed, or the preflight refused
	}
	switchedMsg struct { // a branch switch finished
		owner   *gitActions
		project string
		repo    gitinfo.Repo
		branch  gitinfo.Branch
		ran     bool // git switch ran; false if the preflight refused
		output  string
		err     error
	}
	pullCheckMsg struct { // the repository was checked before asking to pull
		owner   *gitActions
		project string
		repo    gitinfo.Repo
		status  gitinfo.Status
		err     error // reading failed, or the preflight refused
	}
	pulledMsg struct { // a pull finished
		owner         *gitActions
		project       string
		repo          gitinfo.Repo
		ran           bool // git pull ran; false if the preflight refused
		before, after gitinfo.Status
		output        string
		err           error
	}
)

// gitHost is the screen git actions run in: the launcher or the running
// view (see launcherGit and runningGit).
type gitHost interface {
	tracker() *gitTracker // nil: git is not installed
	label(project string) string
	styledLabel(project string) string // bold, in the project's color
	// sharing returns the other projects on the screen that are in the
	// repository at root.
	sharing(project, root string) []string
	// busy reports whether something else has the screen; a reply that
	// would open a picker or a question is dropped then.
	busy() bool
	setNotice(notice string)
	openPicker(p *picker)
	askQuestion(q *question)
	// note adds lines to the project's log, reporting whether it has one.
	note(project string, lines ...string) bool
	// showOutput shows git's output of failed or skipped actions until the
	// next key, unless the screen has it in the logs already.
	showOutput(lines []string, skippedOnly bool)
	// refresh reads the git state again after an action changed it.
	refresh() tea.Cmd
}

// gitActions runs the git actions of one screen, one at a time.
type gitActions struct {
	host   gitHost
	st     *styles
	cfg    *config.Config
	action string // the git action running in the background, e.g. "git switch"; "" if none
}

func newGitActions(host gitHost, st *styles, cfg *config.Config) *gitActions {
	return &gitActions{host: host, st: st, cfg: cfg}
}

// update handles a git action's reply if it is for a, and drops it if it is
// for another screen's actions (e.g. sent before the running view replaced
// the launcher). It reports whether msg was such a reply.
func (a *gitActions) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case branchesMsg:
		if msg.owner == a {
			return a.showBranches(msg), true
		}
	case switchedMsg:
		if msg.owner == a {
			return a.switched(msg), true
		}
	case pullCheckMsg:
		if msg.owner == a {
			a.confirmPull(msg)
		}
	case pulledMsg:
		if msg.owner == a {
			return a.pulled(msg), true
		}
	case fetchedAllMsg:
		if msg.owner == a {
			return a.fetchedAll(msg), true
		}
	case pulledAllMsg:
		if msg.owner == a {
			return a.pulledAll(msg), true
		}
	default:
		return nil, false
	}
	return nil, true
}

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

// target returns the repository of a project for a git action. If there is
// none, or another action is running, it sets a notice saying why.
func (a *gitActions) target(name string) (gitinfo.Repo, bool) {
	repo, notice := a.host.tracker().target(name, a.host.label(name), a.action)
	a.host.setNotice(notice)
	return repo, notice == ""
}

// begin marks action (e.g. "git pull") as running, unless another one is;
// then it sets a notice saying so.
func (a *gitActions) begin(action string) bool {
	if a.action != "" {
		a.host.setNotice(a.action + " is still running")
		return false
	}
	a.action = action
	return true
}

// labels returns names as the screen shows them.
func (a *gitActions) labels(names []string) []string {
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = a.host.label(n)
	}
	return labels
}

// also notes in the logs of the other projects in a repository what an
// action (e.g. "git pull") run for project changed there, and returns the
// notice's suffix naming them, e.g. " (also api, Worker)", or "".
func (a *gitActions) also(project, root, what, action string) string {
	others := a.host.sharing(project, root)
	if len(others) == 0 {
		return ""
	}
	label := a.host.label(project)
	for _, name := range others {
		a.host.note(name, sameRepo(what, action, label))
	}
	return " (also " + strings.Join(a.labels(others), ", ") + ")"
}

// sameRepo is the line for the log of a project that an action (e.g. "git
// pull") run for another (shown as label) changed too.
func sameRepo(what, action, label string) string {
	return fmt.Sprintf("%s (%s run for %s, same repository)", what, action, label)
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

// openBranches checks the project's repository and reads its branches in
// the background; the picker opens when they arrive.
func (a *gitActions) openBranches(project string) tea.Cmd {
	repo, ok := a.target(project)
	if !ok {
		return nil
	}
	return readBranches(a, a.host.tracker().reader, project, repo)
}

// readBranches checks repo and reads its branches in the background, for
// the picker.
func readBranches(owner *gitActions, reader GitReader, project string, repo gitinfo.Repo) tea.Cmd {
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

// showBranches opens the branch picker; every project on the screen in the
// repository switches along.
func (a *gitActions) showBranches(msg branchesMsg) tea.Cmd {
	h := a.host
	if msg.err != nil {
		h.setNotice("can't switch branches: " + msg.err.Error())
		return nil
	}
	if h.busy() {
		return nil // something else took over the screen meanwhile
	}
	others := a.labels(h.sharing(msg.project, msg.repo.Root))
	var dir string
	if p := a.cfg.Projects[msg.project]; p != nil {
		dir = p.Dir
	}
	context := branchContext(a.st, h.styledLabel(msg.project), dir, h.tracker(), msg.project)
	p, cmd := branchPicker(a.st, msg, h.label(msg.project), context, others, func(b gitinfo.Branch) tea.Cmd {
		return a.switchBranch(msg.project, msg.repo, b)
	})
	h.openPicker(p)
	return cmd
}

// switchBranch checks the repository again and switches it to b in the
// background.
func (a *gitActions) switchBranch(project string, repo gitinfo.Repo, b gitinfo.Branch) tea.Cmd {
	if b.Current {
		a.host.setNotice("already on " + b.Name)
		return nil
	}
	if !a.begin("git switch") {
		return nil
	}
	a.host.setNotice(fmt.Sprintf("switching %s to %s…", a.host.label(project), b.LocalName()))
	return runSwitch(a, a.host.tracker().reader, project, repo, b)
}

// runSwitch checks repo again and switches it to b in the background.
func runSwitch(owner *gitActions, reader GitReader, project string, repo gitinfo.Repo, b gitinfo.Branch) tea.Cmd {
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
func (a *gitActions) switched(msg switchedMsg) tea.Cmd {
	h := a.host
	a.action = ""
	if !msg.ran {
		h.setNotice("can't switch branches: " + msg.err.Error())
		return nil
	}
	branch := msg.branch.LocalName()
	lines := switchLines(msg)
	if msg.err != nil {
		h.setNotice(msg.err.Error() + a.failed(msg.project, lines, "git switch failed"))
		return h.refresh()
	}
	h.note(msg.project, append(lines, "switched to "+branch)...)
	notice := fmt.Sprintf("switched %s to %s", h.label(msg.project), branch)
	h.setNotice(notice + a.also(msg.project, msg.repo.Root, "switched to "+branch, "git switch"))
	return h.refresh()
}

// failed shows the lines of a failed action on project, followed in the log
// by last, and returns the notice's suffix pointing to the log, if any.
func (a *gitActions) failed(project string, lines []string, last string) string {
	a.host.showOutput(lines, false)
	if a.host.note(project, append(lines[:len(lines):len(lines)], last)...) {
		return " (output in the log)"
	}
	return ""
}

// openPull checks the project's repository in the background; the question
// whether to pull opens when it is done.
func (a *gitActions) openPull(project string) tea.Cmd {
	repo, ok := a.target(project)
	if !ok {
		return nil
	}
	return checkPull(a, a.host.tracker().reader, project, repo)
}

// checkPull checks repo in the background before asking whether to pull.
func checkPull(owner *gitActions, reader GitReader, project string, repo gitinfo.Repo) tea.Cmd {
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

// confirmPull asks whether to pull, naming the other projects on the screen
// that the pull updates too.
func (a *gitActions) confirmPull(msg pullCheckMsg) {
	h := a.host
	if msg.err != nil {
		h.setNotice("can't pull: " + msg.err.Error())
		return
	}
	if h.busy() {
		return // something else took over the screen meanwhile
	}
	others := a.labels(h.sharing(msg.project, msg.repo.Root))
	h.setNotice("")
	h.askQuestion(pullQuestion(msg, h.label(msg.project), others, func() tea.Cmd { return a.pull(msg.project, msg.repo) }))
}

// pull checks the repository again and pulls in the background.
func (a *gitActions) pull(project string, repo gitinfo.Repo) tea.Cmd {
	if !a.begin("git pull") {
		return nil
	}
	a.host.setNotice(fmt.Sprintf("pulling %s…", a.host.label(project)))
	return runPull(a, a.host.tracker().reader, project, repo)
}

// runPull checks repo again and pulls in the background.
func runPull(owner *gitActions, reader GitReader, project string, repo gitinfo.Repo) tea.Cmd {
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
func (a *gitActions) pulled(msg pulledMsg) tea.Cmd {
	h := a.host
	a.action = ""
	if !msg.ran {
		h.setNotice("can't pull: " + msg.err.Error())
		return nil
	}
	label := h.label(msg.project)
	lines := pullLines(msg)
	if msg.err != nil {
		h.setNotice(msg.err.Error() + a.failed(msg.project, lines, "git pull failed"))
		return h.refresh()
	}
	change := pullChange(msg)
	switch {
	case change == "":
		h.note(msg.project, append(lines, "already up to date")...)
		h.setNotice(fmt.Sprintf("%s is already up to date", label))
	case msg.after.Commit == "": // the state couldn't be read afterwards
		h.note(msg.project, append(lines, "pulled "+change)...)
		h.setNotice(fmt.Sprintf("pulled %s into %s", change, label))
	default:
		h.note(msg.project, append(lines, "pulled "+change)...)
		notice := fmt.Sprintf("pulled %s: %s", label, change)
		h.setNotice(notice + a.also(msg.project, msg.repo.Root, "pulled "+change, "git pull"))
	}
	return h.refresh()
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
