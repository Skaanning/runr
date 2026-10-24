package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
)

func launcherConfig(t *testing.T) *config.Config {
	dir := t.TempDir()
	return &config.Config{
		Projects: map[string]*config.Project{
			"db":  {Dir: dir, Cmd: "go run ."},
			"api": {Dir: dir, Cmd: "go run ./cmd/server", DependsOn: []string{"db"}},
			"web": {Dir: dir + "/gone", Cmd: "npm run dev"},
		},
		Groups: map[string][]string{
			"backend": {"db", "api"},
			"empty":   {},
		},
	}
}

func newTestLauncher(t *testing.T, cfg *config.Config) *launcherModel {
	m := newLauncherModel(cfg, nil)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	return m
}

// launcherScreen returns the body rows without styling, trailing spaces trimmed.
func launcherScreen(m *launcherModel) []string {
	rows := strings.Split(ansi.Strip(m.render()), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return rows
}

func TestLauncherList(t *testing.T) {
	cfg := launcherConfig(t)
	m := newTestLauncher(t, cfg)
	dir := cfg.Projects["db"].Dir
	got := launcherScreen(m)[2:10]
	want := []string{
		"  Groups",
		" ▸ [ ] backend  db, api",
		"   [ ] empty    (empty)",
		"",
		"  Projects",
		"   [ ] api      " + pad(ansi.Truncate(dir, 32, "…"), 32) + "  go run ./cmd/server · needs db",
		"   [ ] db       " + pad(ansi.Truncate(dir, 32, "…"), 32) + "  go run .",
		"   [ ] web      " + ansi.Truncate(dir+"/gone", 32, "…") + "  dir does not exist",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("list:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if help := launcherScreen(m)[13]; help != " ↑↓ move  space select  enter start  n new  e edit  g group  d delete  q quit" {
		t.Fatalf("help = %q", help)
	}
}

func TestLauncherSelection(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	m.Update(key("space")) // backend → db, api
	if got := m.selection(); !slices.Equal(got, []string{"backend"}) {
		t.Fatalf("selection = %q", got)
	}
	rows := launcherScreen(m)
	if !strings.Contains(rows[3], "[x] backend") || !strings.Contains(rows[7], "[x] api") || !strings.HasSuffix(rows[0], "2 selected") {
		t.Fatalf("rows:\n%s", strings.Join(rows, "\n"))
	}

	// Unselecting a member leaves the group partly selected.
	m.Update(key("down"))
	m.Update(key("down"))
	m.Update(key("space")) // api
	if got := m.selection(); !slices.Equal(got, []string{"db"}) {
		t.Fatalf("selection = %q", got)
	}
	if !strings.Contains(launcherScreen(m)[3], "[-] backend") {
		t.Fatalf("row = %q", launcherScreen(m)[3])
	}

	m.Update(key("up"))
	m.Update(key("space")) // empty group: nothing to select
	m.Update(key("up"))
	m.Update(key("space")) // partly selected group → select all
	if got := m.selection(); !slices.Equal(got, []string{"backend"}) {
		t.Fatalf("selection = %q", got)
	}
	m.Update(key("space")) // fully selected → unselect all
	if got := m.selection(); len(got) != 0 {
		t.Fatalf("selection = %q", got)
	}
}

func TestLauncherEnter(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	_, cmd := m.Update(key("enter")) // nothing selected: the item under the cursor
	if msg := cmd().(startMsg); !slices.Equal(msg.names, []string{"backend"}) {
		t.Fatalf("names = %q", msg.names)
	}
	for range 5 {
		m.Update(key("down"))
	}
	m.Update(key("space")) // web
	m.Update(key("up"))
	m.Update(key("up"))
	m.Update(key("space")) // api
	_, cmd = m.Update(key("enter"))
	if msg := cmd().(startMsg); !slices.Equal(msg.names, []string{"api", "web"}) {
		t.Fatalf("names = %q", msg.names)
	}
}

func TestLauncherQuit(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		m := newTestLauncher(t, launcherConfig(t))
		if _, cmd := m.Update(key(k)); cmd == nil || cmd() != tea.Quit() {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestLauncherEmpty(t *testing.T) {
	m := newTestLauncher(t, &config.Config{})
	if got := strings.Join(launcherScreen(m), "\n"); !strings.Contains(got, "runr add <name> --dir <path> -- <command>") {
		t.Fatalf("screen:\n%s", got)
	}
	if _, cmd := m.Update(key("enter")); cmd != nil {
		t.Fatal("enter started something with no projects")
	}
	m.Update(key("space"))
}

func TestLauncherScrolls(t *testing.T) {
	cfg := &config.Config{Projects: map[string]*config.Project{}}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		cfg.Projects[n] = &config.Project{Dir: t.TempDir(), Cmd: "x"}
	}
	m := newLauncherModel(cfg, nil)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 8}) // 4 body rows
	for range 11 {
		m.Update(key("down"))
	}
	if got := launcherScreen(m)[5]; !strings.HasPrefix(got, " ▸ [ ] l") {
		t.Fatalf("last body row = %q", got)
	}
	for range 11 {
		m.Update(key("up"))
	}
	if got := launcherScreen(m)[2]; got != "  Projects" {
		t.Fatalf("first body row = %q", got)
	}
}

func TestAppStartsRunningView(t *testing.T) {
	cfg := launcherConfig(t)
	sess := newFakeSession("db", "api")
	var started [][]string
	fail := true
	app := &appModel{cfg: cfg, launcher: newLauncherModel(cfg, nil), start: func(names []string) (Session, error) {
		started = append(started, names)
		if fail {
			return nil, errors.New("cannot start:\n  project web: dir missing")
		}
		return sess, nil
	}}
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 10})

	app.Update(startMsg{names: []string{"web"}})
	if app.running != nil || app.launcher.notice != "cannot start: project web: dir missing" {
		t.Fatalf("running = %v, notice = %q", app.running != nil, app.launcher.notice)
	}

	fail = false
	_, cmd := app.Update(startMsg{names: []string{"backend"}})
	if app.running == nil || cmd == nil {
		t.Fatal("running view not started")
	}
	if got := screen(app.running)[0]; !strings.HasPrefix(got, " runr · backend ") {
		t.Fatalf("header = %q (size not passed on?)", got)
	}
	app.Update(key("q")) // keys now go to the running view
	if sess.stopAll != 1 {
		t.Fatal("q did not reach the running view")
	}
	if len(started) != 2 {
		t.Fatalf("started = %q", started)
	}
}
