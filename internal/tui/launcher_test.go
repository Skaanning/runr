package tui

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
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
	m := newLauncherModel(cfg, nil, newStyles(terminalTheme))
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
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 15})
	got := launcherScreen(m)[2:13]
	want := []string{
		"   Groups",
		"   ╭─ backend ──────────────────────── 2 ─╮  ╭─ empty ────────────────────────── 0 ─╮",
		"   │ [ ] ● db  ● api                      │  │ [ ] (empty)                          │",
		"   ╰──────────────────────────────────────╯  ╰──────────────────────────────────────╯",
		"",
		"   Projects",
		"   [ ] ● api  " + pad(dir, len(dir)+5) + "  go run ./cmd/server · needs db",
		"   [ ] ● db   " + pad(dir, len(dir)+5) + "  go run .",
		"   [ ] ● web  " + dir + "/gone  dir does not exist",
		"",
		"",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("list:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if help := launcherScreen(m)[14]; help != "  ↑↓←→  move  space  select  enter  start  n  new  e  edit  g  group  d  delete  t  theme  q  quit" {
		t.Fatalf("help = %q", help)
	}
}

func TestShortenPath(t *testing.T) {
	sep := string(filepath.Separator)
	path := strings.Join([]string{"~", "work", "clients", "acme", "api"}, sep)
	for _, tt := range []struct {
		width int
		want  string
	}{
		{40, path},
		{len(path), path},
		{len(path) - 1, "~/…/clients/acme/api"},
		{16, "~/…/acme/api"},
		{11, "~/…/api"},
		{6, "…/api"},
		{3, "…pi"},
		{0, ""},
	} {
		want := strings.ReplaceAll(tt.want, "/", sep)
		if got := shortenPath(path, tt.width); got != want || ansi.StringWidth(got) > tt.width {
			t.Errorf("width %d: %q, want %q", tt.width, got, want)
		}
	}
	if got := shortenPath("/home/someone/projects/api", 20); got != "/…/projects/api" {
		t.Errorf("absolute: %q", got)
	}
}

func TestLauncherProjectColumns(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	// The prefix "   [ ] ● " and three gaps take 15 cells, the command 20.
	for _, tt := range []struct {
		width, name, dir, git int
		want                  [3]int
	}{
		{200, 10, 80, 50, [3]int{10, maxDirWidth, maxLauncherGitWidth}}, // capped
		{100, 10, 30, 20, [3]int{10, 30, 20}},                           // fits
		{100, 10, 50, 20, [3]int{10, 35, 20}},                           // the directory shrinks first
		{80, 10, 50, 30, [3]int{10, minDirWidth, 19}},                   // then the branch
		{50, 10, 50, 30, [3]int{10, minDirWidth, minGitWidth}},          // down to their minimums
		{100, 10, 80, 0, [3]int{10, maxDirWidth - 3, 0}},                // no git column, no gap for it
		{60, 30, 10, 5, [3]int{24, 10, 5}},                              // short values never grow
	} {
		m.width = tt.width
		n, d, g := m.projectColumns(tt.name, tt.dir, tt.git)
		if got := [3]int{n, d, g}; got != tt.want {
			t.Errorf("width %d, %d/%d/%d: %v, want %v", tt.width, tt.name, tt.dir, tt.git, got, tt.want)
		}
	}
}

func TestLauncherSelection(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	m.Update(key("space")) // backend → db, api
	if got := m.selection(); !slices.Equal(got, []string{"backend"}) {
		t.Fatalf("selection = %q", got)
	}
	rows := launcherScreen(m)
	if !strings.Contains(rows[4], "│ [x] ● db  ● api ") || !strings.Contains(rows[8], "[x] ● api") || !strings.Contains(rows[len(rows)-2], "2 selected") {
		t.Fatalf("rows:\n%s", strings.Join(rows, "\n"))
	}

	// Unselecting a member leaves the group partly selected.
	m.Update(key("down")) // api
	m.Update(key("space"))
	if got := m.selection(); !slices.Equal(got, []string{"db"}) {
		t.Fatalf("selection = %q", got)
	}
	if !strings.Contains(launcherScreen(m)[4], "│ [-] ● db") {
		t.Fatalf("row = %q", launcherScreen(m)[4])
	}

	m.Update(key("up"))    // backend
	m.Update(key("right")) // empty
	m.Update(key("space")) // empty group: nothing to select
	m.Update(key("left"))
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
	for range 3 {
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
	m := newLauncherModel(cfg, nil, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 8}) // 4 body rows
	for range 11 {
		m.Update(key("down"))
	}
	if got := launcherScreen(m)[5]; !strings.HasPrefix(got, " ▸ [ ] ● l") {
		t.Fatalf("last body row = %q", got)
	}
	for range 11 {
		m.Update(key("up"))
	}
	if got := launcherScreen(m)[2]; got != "   Projects" {
		t.Fatalf("first body row = %q", got)
	}
}

func TestAppStartsRunningView(t *testing.T) {
	cfg := launcherConfig(t)
	sess := newFakeSession("db", "api")
	var started [][]string
	fail := true
	app := &appModel{cfg: cfg, launcher: newLauncherModel(cfg, nil, newStyles(terminalTheme)), start: func(names []string) (Session, error) {
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
	if got := statusRow(app.running); !strings.HasPrefix(got, "  FOLLOW  backend ") {
		t.Fatalf("status bar = %q (size not passed on?)", got)
	}
	app.Update(key("q")) // keys now go to the running view
	if sess.stopAll != 1 {
		t.Fatal("q did not reach the running view")
	}
	if len(started) != 2 {
		t.Fatalf("started = %q", started)
	}
}

// newThemeLauncher returns a launcher whose saves are recorded in saved, or
// fail with *fail if set.
func newThemeLauncher(t *testing.T, theme string, saved *[]*config.Config, fail *error) *launcherModel {
	cfg := launcherConfig(t)
	cfg.Settings.ThemeValue = theme
	m := newLauncherModel(cfg, func(c *config.Config) error {
		if *fail != nil {
			return *fail
		}
		*saved = append(*saved, c)
		return nil
	}, newStyles(themeNamed(cfg.Settings.Theme())))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	return m
}

func TestLauncherThemePicker(t *testing.T) {
	var saved []*config.Config
	var fail error
	m := newThemeLauncher(t, "", &saved, &fail)
	m.Update(key("t"))
	if m.picker == nil {
		t.Fatal("t did not open the theme picker")
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 15})
	rows := launcherScreen(m)
	want := []string{
		"›   type to filter" + strings.Repeat(" ", 90) + "5 themes",
		"",
		"▌ terminal     ●●●●●●●  the terminal's own colors" + strings.Repeat(" ", 59) + "current",
		"  catppuccin   ●●●●●●●  mocha · latte",
		"  tokyo-night  ●●●●●●●  night · day",
		"  gruvbox      ●●●●●●●  dark · light",
		"  nord         ●●●●●●●  dark only",
	}
	if got := strings.Join(inFrame(rows[3:10]), "\n"); got != strings.Join(want, "\n") || !strings.HasPrefix(rows[2], "╭─ theme ─") {
		t.Fatalf("picker:\n%s\nwant:\n%s", strings.Join(rows[2:11], "\n"), strings.Join(want, "\n"))
	}
	if status := rows[13]; !strings.HasPrefix(status, "  THEME  ") {
		t.Fatalf("status bar = %q", status)
	}

	// Moving the cursor previews the theme under it.
	m.Update(key("down"))
	if m.st.theme.about != catppuccin.about {
		t.Fatalf("preview = %q, want catppuccin", m.st.theme.about)
	}
	m.Update(key("enter"))
	if m.picker != nil || len(saved) != 1 || saved[0].Settings.ThemeValue != "catppuccin" {
		t.Fatalf("picker open %v, saved %d", m.picker != nil, len(saved))
	}
	if m.cfg.Settings.Theme() != "catppuccin" || m.st.theme.about != catppuccin.about || m.notice != "theme set to catppuccin" {
		t.Fatalf("theme %q, styles %q, notice %q", m.cfg.Settings.Theme(), m.st.theme.about, m.notice)
	}

	// Reopened, the cursor starts on the saved theme.
	m.Update(key("t"))
	if it, _ := m.picker.selected(); it.name != "catppuccin" {
		t.Fatalf("cursor on %q, want catppuccin", it.name)
	}
}

func TestLauncherThemePickerCancel(t *testing.T) {
	var saved []*config.Config
	var fail error
	m := newThemeLauncher(t, "nord", &saved, &fail)
	m.Update(key("t"))
	if it, _ := m.picker.selected(); it.name != "nord" {
		t.Fatalf("cursor on %q, want nord", it.name)
	}
	// Filtering previews the first match.
	m.Update(key("g"))
	m.Update(key("r"))
	if m.st.theme.about != gruvbox.about {
		t.Fatalf("preview = %q, want gruvbox", m.st.theme.about)
	}
	m.Update(key("esc"))
	if m.picker != nil || len(saved) != 0 || m.cfg.Settings.Theme() != "nord" || m.st.theme.about != nord.about {
		t.Fatalf("picker open %v, saved %d, theme %q, styles %q", m.picker != nil, len(saved), m.cfg.Settings.Theme(), m.st.theme.about)
	}
}

func TestLauncherThemePickerSaveFails(t *testing.T) {
	var saved []*config.Config
	fail := errors.New("permission denied")
	m := newThemeLauncher(t, "", &saved, &fail)
	m.Update(key("t"))
	m.Update(key("up")) // stays on terminal
	m.Update(key("down"))
	m.Update(key("down")) // tokyo-night
	m.Update(key("enter"))
	if m.cfg.Settings.ThemeValue != "" || m.st.theme.about != terminalTheme.about {
		t.Fatalf("theme %q, styles %q, want the terminal theme kept", m.cfg.Settings.ThemeValue, m.st.theme.about)
	}
	if m.notice != "can't save the theme: permission denied" {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestLauncherThemePicker16Colors(t *testing.T) {
	var saved []*config.Config
	var fail error
	m := newThemeLauncher(t, "", &saved, &fail)
	m.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI})
	m.Update(key("t"))
	rows := inFrame(launcherScreen(m)[3:11])
	if rows[2] != "this terminal shows 16 colors, so runr uses the terminal theme here" {
		t.Fatalf("note = %q", rows[2])
	}
	if rows[5] != "  catppuccin   mocha · latte" {
		t.Fatalf("row = %q (no swatch on 16 colors)", rows[5])
	}
}

// inFrame returns rows of a framed pane without the frame, trailing spaces
// trimmed.
func inFrame(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		r = strings.TrimSuffix(strings.TrimPrefix(r, "│ "), "│")
		out[i] = strings.TrimRight(r, " ")
	}
	return out
}
