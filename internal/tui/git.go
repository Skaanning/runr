package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"runr/internal/config"
	"runr/internal/gitinfo"
)

// GitReader reads the git state of project directories and runs the git
// actions (*gitinfo.Git).
type GitReader interface {
	FindAll(ctx context.Context, dirs map[string]string) gitinfo.Repos
	Status(ctx context.Context, repo gitinfo.Repo) (gitinfo.Status, error)
	Branches(ctx context.Context, repo gitinfo.Repo) ([]gitinfo.Branch, error)
	Switch(ctx context.Context, repo gitinfo.Repo, b gitinfo.Branch) (string, error)
}

const (
	gitInterval = 5 * time.Second // between background refreshes
	maxGitWidth = 24              // widest git column; longer branches are truncated
	minGitWidth = 8               // narrower than this, the git column is hidden
)

// projectGit is the git state of a project's directory.
type projectGit struct {
	root   string // repository root
	status gitinfo.Status
	err    error // the state could not be read
}

type (
	gitMsg struct { // a refresh finished
		owner *gitTracker
		repos gitinfo.Repos
		state map[string]projectGit
	}
	gitTickMsg struct { // time for the next refresh
		owner *gitTracker
		gen   int
	}
)

// gitTracker keeps the git state of a set of projects up to date. Refreshes
// run in the background, one at a time, and every gitInterval after the last
// one finished; a refresh requested while one runs follows it.
//
// A nil *gitTracker (git is not installed) does nothing.
type gitTracker struct {
	reader GitReader
	state  map[string]projectGit // by project; projects outside a repo are absent
	repos  gitinfo.Repos
	busy   bool // a refresh is running
	again  bool // refresh again once it finishes
	gen    int  // generation of the scheduled tick; older ticks are ignored
}

func newGitTracker(r GitReader) *gitTracker {
	if r == nil {
		return nil
	}
	return &gitTracker{reader: r, state: map[string]projectGit{}}
}

// get returns the git state of a project, and whether it is in a repository.
func (t *gitTracker) get(name string) (projectGit, bool) {
	if t == nil {
		return projectGit{}, false
	}
	g, ok := t.state[name]
	return g, ok
}

// refresh reads the git state of projects (name → directory) in the
// background, querying each repository once.
func (t *gitTracker) refresh(dirs map[string]string) tea.Cmd {
	if t == nil {
		return nil
	}
	if t.busy {
		t.again = true
		return nil
	}
	t.busy = true
	reader := t.reader
	return func() tea.Msg {
		ctx := context.Background()
		repos := reader.FindAll(ctx, dirs)
		state := map[string]projectGit{}
		for name, err := range repos.Errors {
			state[name] = projectGit{err: err}
		}
		for root, repo := range repos.ByRoot {
			status, err := reader.Status(ctx, repo)
			for _, name := range repos.Members[root] {
				state[name] = projectGit{root: root, status: status, err: err}
			}
		}
		return gitMsg{owner: t, repos: repos, state: state}
	}
}

// update handles the tracker's messages and returns what to do next: the
// next refresh, or the tick that triggers it. dirs is called for the
// projects to refresh. Messages of other trackers are ignored.
func (t *gitTracker) update(msg tea.Msg, dirs func() map[string]string) tea.Cmd {
	switch msg := msg.(type) {
	case gitMsg:
		if t == nil || msg.owner != t {
			return nil
		}
		t.state, t.repos, t.busy = msg.state, msg.repos, false
		if t.again {
			t.again = false
			return t.refresh(dirs())
		}
		t.gen++
		gen := t.gen
		return tea.Tick(gitInterval, func(time.Time) tea.Msg { return gitTickMsg{owner: t, gen: gen} })
	case gitTickMsg:
		if t == nil || msg.owner != t || msg.gen != t.gen {
			return nil
		}
		return t.refresh(dirs())
	}
	return nil
}

// projectDirs returns the resolved directories of the named projects.
func projectDirs(cfg *config.Config, names []string) map[string]string {
	dirs := map[string]string{}
	for _, name := range names {
		if p := cfg.Projects[name]; p != nil {
			if dir, err := p.ResolvedDir(); err == nil {
				dirs[name] = dir
			}
		}
	}
	return dirs
}

// gitParts returns the branch (or detached commit) and the change markers
// for a project's git state, unstyled.
func gitParts(g projectGit) (branch string, markers []string) {
	if g.err != nil {
		return "?", nil
	}
	s := g.status
	branch = s.Branch
	if s.Detached {
		branch = "(" + s.ShortCommit() + ")"
	}
	if s.Changed {
		markers = append(markers, "*")
	}
	if s.Ahead > 0 {
		markers = append(markers, "↑"+strconv.Itoa(s.Ahead))
	}
	if s.Behind > 0 {
		markers = append(markers, "↓"+strconv.Itoa(s.Behind))
	}
	if s.Conflicts || s.Operation != gitinfo.NoOperation {
		markers = append(markers, "!")
	}
	return branch, markers
}

// gitWidth is the width of a project's git label without truncation.
func gitWidth(g projectGit) int {
	branch, markers := gitParts(g)
	w := ansi.StringWidth(branch)
	for _, mk := range markers {
		w += 1 + ansi.StringWidth(mk)
	}
	return w
}

// gitLabel renders a project's git state in at most width cells. The branch
// is truncated first, so the markers stay visible.
func gitLabel(g projectGit, width int) string {
	branch, markers := gitParts(g)
	if g.err != nil {
		return faintStyle.Render(branch)
	}
	plainMarkers := strings.Join(markers, " ")
	if mw := ansi.StringWidth(plainMarkers); mw > 0 && mw+2 <= width {
		branch = ansi.Truncate(branch, width-mw-1, "…")
	} else if mw > 0 {
		markers = nil // not even the markers fit next to a branch
	}
	branch = ansi.Truncate(branch, width, "…")
	out := branch
	for _, mk := range markers {
		style := plainStyle
		switch mk {
		case "*":
			style = warnStyle
		case "!":
			style = errStyle
		}
		out += " " + style.Render(mk)
	}
	return out
}
