package tui

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/gitinfo"
	"github.com/Skaanning/runr/internal/supervisor"
)

// fakeGit serves canned git state: projects map to repository roots.
type fakeGit struct {
	of       map[string]string         // project → root
	status   map[string]gitinfo.Status // by root
	errs     map[string]error          // by root (Status) or project (lookup)
	finds    []map[string]string       // dirs passed to each FindAll
	statuses []string                  // roots passed to Status

	branches  map[string][]gitinfo.Branch // by root
	switches  []string                    // "root branch" for each Switch call
	switchOut string                      // output of Switch
	switchErr error                       // returned by Switch

	pulls    []string                  // root of each Pull call
	pullOut  string                    // output of Pull
	pullErr  error                     // returned by Pull
	pulledTo map[string]gitinfo.Status // by root: status after a successful Pull
}

func (f *fakeGit) FindAll(_ context.Context, dirs map[string]string) gitinfo.Repos {
	f.finds = append(f.finds, dirs)
	r := gitinfo.Repos{ByRoot: map[string]gitinfo.Repo{}, Members: map[string][]string{}, Of: map[string]string{}, Errors: map[string]error{}}
	for _, name := range slices.Sorted(maps.Keys(dirs)) {
		if err := f.errs[name]; err != nil {
			r.Errors[name] = err
			continue
		}
		root, ok := f.of[name]
		if !ok {
			continue
		}
		r.ByRoot[root] = gitinfo.Repo{Root: root}
		r.Members[root] = append(r.Members[root], name)
		r.Of[name] = root
	}
	return r
}

func (f *fakeGit) Status(_ context.Context, repo gitinfo.Repo) (gitinfo.Status, error) {
	f.statuses = append(f.statuses, repo.Root)
	return f.status[repo.Root], f.errs[repo.Root]
}

func (f *fakeGit) Branches(_ context.Context, repo gitinfo.Repo) ([]gitinfo.Branch, error) {
	return f.branches[repo.Root], nil
}

func (f *fakeGit) Switch(_ context.Context, repo gitinfo.Repo, b gitinfo.Branch) (string, error) {
	f.switches = append(f.switches, repo.Root+" "+b.Name)
	if f.switchErr == nil {
		s := f.status[repo.Root]
		s.Branch = b.LocalName()
		f.status[repo.Root] = s
	}
	return f.switchOut, f.switchErr
}

func (f *fakeGit) Pull(_ context.Context, repo gitinfo.Repo) (string, error) {
	f.pulls = append(f.pulls, repo.Root)
	if s, ok := f.pulledTo[repo.Root]; ok && f.pullErr == nil {
		f.status[repo.Root] = s
	}
	return f.pullOut, f.pullErr
}

func testGit() *fakeGit {
	return &fakeGit{
		of: map[string]string{"db": "/r/infra", "api": "/r/app", "worker": "/r/app"},
		status: map[string]gitinfo.Status{
			"/r/infra": {Branch: "main", Commit: "a1b2c3d4e5"},
			"/r/app":   {Branch: "feat/login", Changed: true, Behind: 2},
		},
	}
}

// loadGit runs one refresh of the model's tracker synchronously.
func loadGit(t *testing.T, update func(tea.Msg), tr *gitTracker, dirs map[string]string) {
	t.Helper()
	cmd := tr.refresh(dirs)
	if cmd == nil {
		t.Fatal("refresh returned no command")
	}
	update(cmd())
}

func TestRunningSidebarGit(t *testing.T) {
	m, _ := newTestModel(t, 80, 7)
	m.git = newGitTracker(testGit())
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running), line("api", "hello")}})

	want := []string{
		" runr · backend                                            combined · following",
		"──────────────────────────┬─────────────────────────────────────────────────────",
		" ▸ ● db         main      │ api    hello",
		"   ○ api        fea… * ↓2 │",
		"   ○ Worker     fea… * ↓2 │",
	}
	if got := screen(m)[:5]; !slices.Equal(got, want) {
		t.Fatalf("screen:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRunningSidebarGitWidths(t *testing.T) {
	tests := []struct {
		width int
		row   string // the api row
	}{
		{120, "   ○ api        feat/login * ↓2 │"},
		{75, "   ○ api        fe… * ↓2 │"},
		{60, "   ○ api        │"}, // no room: the column is dropped, names stay whole
	}
	for _, tt := range tests {
		m, _ := newTestModel(t, tt.width, 7)
		m.git = newGitTracker(testGit())
		loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
		if got := screen(m)[3]; got != tt.row {
			t.Errorf("width %d: row %q, want %q", tt.width, got, tt.row)
		}
		for i, row := range strings.Split(m.render(), "\n") {
			if w := ansi.StringWidth(row); w > tt.width {
				t.Errorf("width %d: row %d is %d cells", tt.width, i, w)
			}
		}
	}
}

func TestRunningWithoutGit(t *testing.T) {
	m, _ := newTestModel(t, 120, 7)
	if m.git != nil || m.Init() == nil {
		t.Fatal("the model should work without a git tracker")
	}
	if w, col := m.sidebarLayout(); col != 0 || w != 5+6+codeWidth+1 {
		t.Errorf("layout = %d, %d", w, col)
	}
}

func TestGitLabel(t *testing.T) {
	tests := []struct {
		g     projectGit
		width int
		want  string
	}{
		{projectGit{status: gitinfo.Status{Branch: "main"}}, 24, "main"},
		{projectGit{status: gitinfo.Status{Branch: "main", Changed: true, Ahead: 1, Behind: 12, Conflicts: true}}, 24, "main * ↑1 ↓12 !"},
		{projectGit{status: gitinfo.Status{Branch: "main", Operation: gitinfo.Rebase}}, 24, "main !"},
		{projectGit{status: gitinfo.Status{Detached: true, Commit: "a1b2c3d4e5f6"}}, 24, "(a1b2c3d)"},
		{projectGit{status: gitinfo.Status{Branch: "feature/very-long-name", Ahead: 3}}, 12, "feature/… ↑3"},
		{projectGit{status: gitinfo.Status{Branch: "feature", Ahead: 3, Behind: 4}}, 6, "featu…"}, // markers don't fit
		{projectGit{err: errors.New("dubious ownership")}, 24, "?"},
	}
	for _, tt := range tests {
		got := ansi.Strip(gitLabel(newStyles(terminalPalette), tt.g, tt.width))
		if got != tt.want {
			t.Errorf("gitLabel(%+v, %d) = %q, want %q", tt.g, tt.width, got, tt.want)
		}
		if w := ansi.StringWidth(got); w > tt.width {
			t.Errorf("%q is wider than %d", got, tt.width)
		}
	}
}

func TestGitTrackerQueriesEachRepoOnce(t *testing.T) {
	f := testGit()
	tr := newGitTracker(f)
	loadGit(t, func(msg tea.Msg) { tr.update(msg, nil) }, tr, map[string]string{"db": "/r/infra", "api": "/r/app/api", "worker": "/r/app/worker", "notes": "/n"})
	if got := slices.Sorted(slices.Values(f.statuses)); !slices.Equal(got, []string{"/r/app", "/r/infra"}) {
		t.Errorf("Status calls = %v", f.statuses)
	}
	if g, ok := tr.get("worker"); !ok || g.root != "/r/app" || g.status.Branch != "feat/login" {
		t.Errorf("worker = %+v, %v", g, ok)
	}
	if _, ok := tr.get("notes"); ok {
		t.Error("project outside a repo has git state")
	}
}

func TestGitTrackerScheduling(t *testing.T) {
	f := testGit()
	tr := newGitTracker(f)
	dirs := func() map[string]string { return map[string]string{"db": "/r/infra"} }

	first := tr.refresh(dirs())
	if tr.refresh(dirs()) != nil {
		t.Fatal("a second refresh started while one is running")
	}
	// The refresh requested meanwhile runs as soon as the first finishes.
	again := tr.update(first(), dirs)
	if again == nil || !tr.busy {
		t.Fatal("the requested refresh did not follow")
	}
	if tick := tr.update(again(), dirs); tick == nil || tr.busy || tr.gen != 1 {
		t.Fatalf("no tick scheduled after the refresh (gen %d)", tr.gen)
	}
	// Only the newest tick refreshes.
	if tr.update(gitTickMsg{owner: tr, gen: 0}, dirs) != nil {
		t.Error("a stale tick refreshed")
	}
	if tr.update(gitTickMsg{owner: tr, gen: 1}, dirs) == nil {
		t.Error("the current tick did not refresh")
	}

	// Another tracker's messages are ignored.
	other := newGitTracker(f)
	if tr.update(gitTickMsg{owner: other, gen: 1}, dirs) != nil {
		t.Error("refreshed on another tracker's tick")
	}
	before := tr.state
	tr.update(gitMsg{owner: other, state: map[string]projectGit{}}, dirs)
	if len(tr.state) != len(before) {
		t.Error("took another tracker's state")
	}
	if len(f.finds) != 2 {
		t.Errorf("FindAll called %d times", len(f.finds))
	}
}

func TestGitTickInterval(t *testing.T) {
	if gitInterval != 5*time.Second {
		t.Errorf("gitInterval = %v, the spec says 5s", gitInterval)
	}
}

func TestRunningRefreshesGitForNewProjects(t *testing.T) {
	m, _ := newTestModel(t, 120, 7)
	f := testGit()
	m.git = newGitTracker(f)
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())

	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running)}})
	if m.git.busy {
		t.Fatal("refreshed without a new project")
	}
	m.cfg.Projects["cache"] = m.cfg.Projects["db"]
	m.Update(eventsMsg{events: []supervisor.Event{state("cache", supervisor.Waiting)}})
	if !m.git.busy {
		t.Fatal("no refresh after a project was added")
	}
}

func TestRunningStopsGitTicksWhenStopping(t *testing.T) {
	m, _ := newTestModel(t, 120, 7)
	m.git = newGitTracker(testGit())
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())
	m.Update(key("q"))
	if _, cmd := m.Update(gitTickMsg{owner: m.git, gen: m.git.gen}); cmd != nil || m.git.busy {
		t.Error("refreshed git state while stopping")
	}
}

func TestLauncherShowsGit(t *testing.T) {
	cfg := testConfig()
	m := newLauncherModel(cfg, nil, newStyles(terminalPalette))
	m.git = newGitTracker(testGit())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	loadGit(t, func(msg tea.Msg) { m.Update(msg) }, m.git, m.gitDirs())

	rows := strings.Split(ansi.Strip(m.render()), "\n")
	var api string
	for _, r := range rows {
		if strings.Contains(r, " api ") {
			api = r
		}
	}
	if !strings.Contains(api, "feat/login * ↓2  api") {
		t.Errorf("api row = %q", api)
	}
}
