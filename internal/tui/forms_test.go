package tui

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
)

func TestParseEnv(t *testing.T) {
	env, err := parseEnv("A=1\n\n# comment\n B = two words \nEMPTY=\nURL=a=b")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": " two words", "EMPTY": "", "URL": "a=b"}
	if !maps.Equal(env, want) {
		t.Fatalf("env = %q, want %q", env, want)
	}
	if env, err := parseEnv("  \n"); err != nil || env != nil {
		t.Fatalf("blank: %v, %v", env, err)
	}
	if _, err := parseEnv("A=1\nnope"); err == nil || err.Error() != "line 2: expected KEY=VALUE" {
		t.Fatalf("err = %v", err)
	}
}

func TestProjectValuesCheck(t *testing.T) {
	cfg := launcherConfig(t)
	dir := cfg.Projects["db"].Dir
	tests := []struct {
		name  string
		edit  func(v *projectValues)
		field string
		want  string // "" for no error
	}{
		{"valid", func(v *projectValues) {}, "", ""},
		{"name required", func(v *projectValues) { v.name = " " }, "", "is required"},
		{"name taken", func(v *projectValues) { v.name = "db" }, "", "a project or group with this name already exists"},
		{"name used by group", func(v *projectValues) { v.name = "backend" }, "", "a project or group with this name already exists"},
		{"bad name", func(v *projectValues) { v.name = "-x" }, "", "invalid name"},
		{"cmd required", func(v *projectValues) { v.cmd = "" }, "cmd", "is required"},
		{"bad color", func(v *projectValues) { v.color = "puce" }, "color", `unknown color "puce"`},
		{"bad regex", func(v *projectValues) { v.readyLog = "(" }, "ready_log", "invalid regex"},
		{"bad env", func(v *projectValues) { v.env = "X" }, "env", "line 1: expected KEY=VALUE"},
		{"bad env key", func(v *projectValues) { v.env = "=1" }, "env", "invalid variable name"},
		{"env error not on other fields", func(v *projectValues) { v.env = "X" }, "cmd", ""},
		{"missing dir is only a warning", func(v *projectValues) { v.dir = dir + "/nope" }, "dir", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newProjectValues(cfg, "")
			v.name, v.cmd, v.dir = "worker", "run", dir
			tt.edit(v)
			err := v.check(cfg, tt.field)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestProjectValuesCycle(t *testing.T) {
	cfg := launcherConfig(t) // api depends on db
	v := newProjectValues(cfg, "db")
	v.deps = []string{"api"}
	if err := v.check(cfg, "depends_on"); err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("err = %v", err)
	}
}

func TestProjectValuesEditRoundTrip(t *testing.T) {
	cfg := launcherConfig(t)
	cfg.Projects["api"].Env = map[string]string{"B": "2", "A": "1"}
	cfg.Projects["api"].Color = "cyan"
	v := newProjectValues(cfg, "api")
	if v.env != "A=1\nB=2" || v.original != "api" {
		t.Fatalf("values = %+v", v)
	}
	c, err := v.candidate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := *c.Projects["api"], *cfg.Projects["api"]; got.Cmd != want.Cmd || got.Dir != want.Dir ||
		!maps.Equal(got.Env, want.Env) || !slices.Equal(got.DependsOn, want.DependsOn) || got.Color != want.Color {
		t.Fatalf("project changed: %+v, want %+v", got, want)
	}
	if cfg.Projects["api"] == c.Projects["api"] {
		t.Fatal("candidate shares the project with the original config")
	}
}

func TestProjectValuesNormalizesDir(t *testing.T) {
	cfg := launcherConfig(t)
	v := newProjectValues(cfg, "")
	v.name, v.cmd, v.dir = "x", "run", "relative/dir"
	c, err := v.candidate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Projects["x"].Dir; !filepath.IsAbs(d) || !strings.HasSuffix(d, filepath.FromSlash("relative/dir")) {
		t.Fatalf("dir = %q", d)
	}
	for _, dir := range []string{"~/code", "~/code/"} {
		v.dir = dir
		if c, _ := v.candidate(cfg); c.Projects["x"].Dir != "~/code" {
			t.Fatalf("%q: dir = %q", dir, c.Projects["x"].Dir)
		}
	}
}

func TestProjectValuesUnderRunrRoot(t *testing.T) {
	cfg := launcherConfig(t)
	root := t.TempDir()
	api := filepath.Join(root, "api")
	if err := os.Mkdir(api, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.RootEnvVar, root)

	v := newProjectValues(cfg, "")
	v.name, v.cmd, v.dir = "x", "run", api
	if c, err := v.candidate(cfg); err != nil || c.Projects["x"].Dir != "$RUNR_ROOT/api" {
		t.Fatalf("typed absolute: %v", err)
	}
	v.dir = "$RUNR_ROOT/api"
	if c, err := v.candidate(cfg); err != nil || c.Projects["x"].Dir != "$RUNR_ROOT/api" {
		t.Fatalf("typed in root form: %v", err)
	}
	if check := checkDir(nil, "x", "$RUNR_ROOT/api", ""); check.err != nil {
		t.Fatalf("check: %v", check.err)
	}
	if got := browseStart("$RUNR_ROOT/api/nope"); got != api {
		t.Fatalf("browseStart = %q, want %q", got, api)
	}

	t.Setenv(config.RootEnvVar, "")
	if check := checkDir(nil, "x", "$RUNR_ROOT/api", ""); check.err == nil || !strings.Contains(check.err.Error(), "$RUNR_ROOT is not set") {
		t.Fatalf("check with the root unset: %v", check.err)
	}
}

func TestGroupValues(t *testing.T) {
	cfg := launcherConfig(t)
	cfg.Groups["backend"] = []string{"db", "api"}
	v := &groupValues{original: "backend", name: "backend", members: []string{"api", "web", "db"}}
	if c, _ := v.candidate(cfg); !slices.Equal(c.Groups["backend"], []string{"db", "api", "web"}) {
		t.Fatalf("members = %q, want the existing order kept", c.Groups["backend"])
	}
	v = &groupValues{name: "jobs"}
	if c, _ := v.candidate(cfg); c.Groups["jobs"] == nil || len(c.Groups["jobs"]) != 0 {
		t.Fatalf("empty group = %#v", c.Groups["jobs"])
	}

	for name, want := range map[string]string{
		"":        "is required",
		"backend": "already exists",
		"db":      "already exists",
		"-x":      "invalid name",
		"jobs":    "",
	} {
		err := (&groupValues{name: name}).checkName(cfg)
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("checkName(%q) = %v, want %q", name, err, want)
		}
	}
}

func TestDeleteConfirmation(t *testing.T) {
	cfg := launcherConfig(t)
	c := newDeleteConfirmation(cfg, launcherItem{name: "db"})
	want := []string{
		`Delete project "db"?`,
		"It is also removed from: backend",
		"Required by api; it is removed from their depends_on.",
	}
	if !slices.Equal(c.lines, want) {
		t.Fatalf("lines = %q", c.lines)
	}
	if cfg.Projects["db"] == nil {
		t.Fatal("the confirmation changed the config")
	}
	after := c.result()
	if after.Projects["db"] != nil || len(after.Projects["api"].DependsOn) != 0 {
		t.Fatalf("after = %+v", after.Projects)
	}

	g := newDeleteConfirmation(cfg, launcherItem{name: "backend", group: true})
	if g.lines[0] != `Delete group "backend"? Its projects are kept.` || g.result().Groups["backend"] != nil {
		t.Fatalf("group: %q", g.lines)
	}
}

// fakeForm returns a form without fields whose result is c.
func fakeForm(c *config.Config, focus launcherItem) *editForm {
	return &editForm{
		title:   "test",
		form:    newForm(newStyles(terminalTheme)),
		result:  func() (*config.Config, launcherItem, error) { return c, focus, nil },
		context: func() string { return "" },
	}
}

// submit opens f, then saves it with ctrl+s, or cancels it with esc.
func submit(m *launcherModel, f *editForm, save bool) {
	m.openForm(f)
	if save {
		m.Update(key("ctrl+s"))
	} else {
		m.Update(key("esc"))
	}
}

func TestLauncherFormSaves(t *testing.T) {
	cfg := launcherConfig(t)
	var saved []*config.Config
	m := newLauncherModel(cfg, func(c *config.Config) error { saved = append(saved, c); return nil }, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	shared := cfg // e.g. the app's pointer

	c := cfg.Clone()
	c.Projects["cron"] = &config.Project{Dir: cfg.Projects["db"].Dir, Cmd: "cron"}
	submit(m, fakeForm(c, launcherItem{name: "cron"}), true)

	if len(saved) != 1 || m.form != nil {
		t.Fatalf("saved = %d, form open = %v", len(saved), m.form != nil)
	}
	if shared.Projects["cron"] == nil {
		t.Fatal("config not updated in place")
	}
	if it, _ := m.current(); it.name != "cron" || m.notice != `saved project "cron"` {
		t.Fatalf("cursor on %q, notice %q", it.name, m.notice)
	}
}

func TestLauncherFormCancelAndErrors(t *testing.T) {
	cfg := launcherConfig(t)
	saveErr := errors.New("disk full")
	calls := 0
	m := newLauncherModel(cfg, func(*config.Config) error { calls++; return saveErr }, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})

	submit(m, fakeForm(cfg.Clone(), launcherItem{}), false)
	if m.form != nil || calls != 0 {
		t.Fatalf("cancel: form open = %v, calls = %d", m.form != nil, calls)
	}

	invalid := cfg.Clone()
	invalid.Projects["bad"] = &config.Project{Dir: "/x"} // no cmd
	submit(m, fakeForm(invalid, launcherItem{name: "bad"}), true)
	if m.form == nil || calls != 0 || !strings.Contains(m.notice, "cmd: is required") {
		t.Fatalf("invalid: form open = %v, calls = %d, notice = %q", m.form != nil, calls, m.notice)
	}

	m.form = nil
	submit(m, fakeForm(cfg.Clone(), launcherItem{name: "db"}), true)
	if m.form == nil || m.notice != "disk full" {
		t.Fatalf("save error: form open = %v, notice = %q", m.form != nil, m.notice)
	}
	m.Update(key("esc"))
	if m.form != nil {
		t.Fatal("esc did not close the form")
	}
}

func TestLauncherKeysOpenForms(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	for k, want := range map[string]string{"n": "new project", "g": "edit backend", "e": "edit backend"} {
		m.Update(key(k))
		if m.form == nil || m.form.title != want {
			t.Fatalf("%s: form = %+v", k, m.form)
		}
		if !strings.HasPrefix(launcherScreen(m)[0], "╭─ "+want+" ─") {
			t.Fatalf("%s: pane top = %q", k, launcherScreen(m)[0])
		}
		m.Update(key("esc"))
	}
	for range 2 {
		m.Update(key("down")) // db
	}
	m.Update(key("space"))
	m.Update(key("e"))
	if m.form.title != "edit db" {
		t.Fatalf("title = %q", m.form.title)
	}
	m.Update(key("esc"))
	m.Update(key("g")) // on a project: new group with the selection
	if m.form.title != "new group" {
		t.Fatalf("title = %q", m.form.title)
	}
	m.Update(key("ctrl+c"))
}

func TestLauncherDeleteFlow(t *testing.T) {
	cfg := launcherConfig(t)
	saves := 0
	m := newLauncherModel(cfg, func(*config.Config) error { saves++; return nil }, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(key("d")) // backend
	if !strings.Contains(strings.Join(launcherScreen(m), "\n"), `Delete group "backend"? Its projects are kept.`) {
		t.Fatalf("screen:\n%s", strings.Join(launcherScreen(m), "\n"))
	}
	if help := launcherScreen(m)[19]; help != "  y  delete  n  cancel" {
		t.Fatalf("help = %q", help)
	}
	m.Update(key("n"))
	if m.confirm != nil || cfg.Groups["backend"] == nil {
		t.Fatal("n did not cancel")
	}
	m.Update(key("d"))
	m.Update(key("y"))
	if m.confirm != nil || cfg.Groups["backend"] != nil || saves != 1 {
		t.Fatalf("delete: confirm open = %v, groups = %v, saves = %d", m.confirm != nil, cfg.Groups, saves)
	}
	if it, _ := m.current(); it.name != "empty" {
		t.Fatalf("cursor on %q", it.name)
	}
}

func TestProjectFormOneShot(t *testing.T) {
	cfg := launcherConfig(t)
	cfg.Projects["db"].Ready = config.ReadyExit
	v := newProjectValues(cfg, "db")
	if !slices.Equal(v.oneShot, []string{oneShotOption}) {
		t.Fatalf("oneShot = %v", v.oneShot)
	}
	c, err := v.candidate(cfg)
	if err != nil || !c.Projects["db"].OneShot() {
		t.Fatalf("candidate: %+v, %v", c.Projects["db"], err)
	}

	v.readyLog = "listening"
	if err := v.check(cfg, "ready"); err == nil || !strings.Contains(err.Error(), "only one readiness condition per project") {
		t.Errorf("one-shot with a ready log: %v", err)
	}
	v.oneShot = nil
	if err := v.check(cfg, "ready"); err != nil {
		t.Errorf("ready log only: %v", err)
	}
	if c, _ := v.candidate(cfg); c.Projects["db"].Ready != "" {
		t.Errorf("Ready = %q after unticking", c.Projects["db"].Ready)
	}

	// The field is in the Startup section and toggles with space.
	m := openProjectForm(t, launcherConfig(t), "api", nil, 96, 30)
	f := m.form.form
	for f.focused().key != "ready" {
		m.Update(key("tab"))
	}
	if !strings.Contains(screenText(m), "One-shot task") {
		t.Fatalf("no one-shot field:\n%s", screenText(m))
	}
	m.Update(key("space"))
	if !strings.Contains(screenText(m), "[x] yes") {
		t.Errorf("not ticked:\n%s", screenText(m))
	}
}

func TestProjectFormReadyPortAndURL(t *testing.T) {
	cfg := launcherConfig(t)
	cfg.Projects["db"].ReadyPort = 5432
	v := newProjectValues(cfg, "db")
	if v.readyPort != "5432" {
		t.Fatalf("readyPort = %q", v.readyPort)
	}
	c, err := v.candidate(cfg)
	if err != nil || c.Projects["db"].ReadyPort != 5432 {
		t.Fatalf("candidate: %+v, %v", c.Projects["db"], err)
	}

	for port, want := range map[string]string{
		"pg":     "not a number",
		"0":      "",
		"99999":  "99999 is not a port",
		" 6543 ": "",
	} {
		v.readyPort = port
		err := v.check(cfg, "ready_port")
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("port %q: %v, want %q", port, err, want)
		}
	}

	v.readyPort, v.readyURL = "5432", "http://localhost:5432"
	if err := v.check(cfg, "ready_url"); err == nil || !strings.Contains(err.Error(), "it already has ready_port") {
		t.Errorf("port and URL: %v", err)
	}
	v.readyPort = ""
	if err := v.check(cfg, "ready_url"); err != nil {
		t.Errorf("URL only: %v", err)
	}
	v.readyURL = "localhost:5432"
	if err := v.check(cfg, "ready_url"); err == nil || !strings.Contains(err.Error(), "must start with http://") {
		t.Errorf("URL without scheme: %v", err)
	}
}

func TestProjectFormRename(t *testing.T) {
	cfg := launcherConfig(t) // api depends on db; backend = db, api
	v := newProjectValues(cfg, "db")
	if err := v.check(cfg, ""); err != nil {
		t.Fatalf("unchanged name: %v", err)
	}
	v.name = "postgres"
	if err := v.check(cfg, ""); err != nil {
		t.Fatal(err)
	}
	c, err := v.candidate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Projects["db"] != nil || c.Projects["postgres"] == nil ||
		!slices.Equal(c.Projects["api"].DependsOn, []string{"postgres"}) ||
		!slices.Equal(c.Groups["backend"], []string{"postgres", "api"}) {
		t.Fatalf("after rename: %+v %v", c.Projects, c.Groups)
	}
	if cfg.Projects["db"] == nil {
		t.Fatal("the original config changed")
	}

	for name, want := range map[string]string{
		"api":     "a project or group with this name already exists",
		"backend": "a project or group with this name already exists",
		"bad one": "invalid name",
		"":        "is required",
	} {
		v.name = name
		if err := v.check(cfg, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("rename to %q: %v, want %q", name, err, want)
		}
		if err := v.check(cfg, "cmd"); err != nil {
			t.Errorf("rename to %q reported on cmd: %v", name, err)
		}
	}
}

func TestGroupFormRename(t *testing.T) {
	cfg := launcherConfig(t)
	v := &groupValues{original: "backend", name: "server", members: []string{"db"}}
	if err := v.checkName(cfg); err != nil {
		t.Fatal(err)
	}
	c, err := v.candidate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Groups["backend"] != nil || !slices.Equal(c.Groups["server"], []string{"db"}) {
		t.Fatalf("groups = %v", c.Groups)
	}
	v.name = "api"
	if err := v.checkName(cfg); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	v.name = "backend"
	if err := v.checkName(cfg); err != nil {
		t.Fatalf("unchanged name: %v", err)
	}
}

func TestLauncherRenameKeepsSelection(t *testing.T) {
	cfg := launcherConfig(t)
	m := newLauncherModel(cfg, nil, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.selected["db"] = true

	c := cfg.Clone()
	if _, _, err := c.Rename("db", "postgres"); err != nil {
		t.Fatal(err)
	}
	f := fakeForm(c, launcherItem{name: "postgres"})
	f.original = launcherItem{name: "db"}
	submit(m, f, true)

	if !m.selected["postgres"] || m.selected["db"] {
		t.Fatalf("selected = %v", m.selected)
	}
	if m.notice != `saved project "postgres" (renamed from "db")` {
		t.Fatalf("notice = %q", m.notice)
	}
	if it, _ := m.current(); it.name != "postgres" {
		t.Fatalf("cursor on %q", it.name)
	}
}

// openProjectForm opens the form for a project ("" for a new one) in a
// launcher of width×height, with git state from g (nil: no git).
func openProjectForm(t *testing.T, cfg *config.Config, name string, g GitReader, width, height int) *launcherModel {
	t.Helper()
	m := newLauncherModel(cfg, nil, newStyles(terminalTheme))
	if g != nil {
		m.git = newGitTracker(g)
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	f := newProjectForm(m.st, cfg, name, m.gitReader())
	if cmd := m.openForm(f); cmd != nil {
		runCmds(m, cmd)
	}
	return m
}

// runCmds runs cmd and feeds its messages back into m, skipping ticks.
func runCmds(m tea.Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runCmds(m, c)
		}
	case dirCheckMsg, nil:
	default:
		_, next := m.Update(msg)
		runCmds(m, next)
	}
}

func typeInto(m tea.Model, s string) {
	for _, r := range s {
		m.Update(key(string(r)))
	}
}

func screenText(m *launcherModel) string {
	return strings.Join(launcherScreen(m), "\n")
}

func TestProjectFormLayout(t *testing.T) {
	cfg := launcherConfig(t)
	m := openProjectForm(t, cfg, "api", nil, 96, 30)
	rows := launcherScreen(m)
	for i, want := range map[int]string{
		0:  "╭─ edit api ─",
		2:  "│ Basics                                         Environment",
		4:  "│ ┃ Name                                           Variables",
		5:  "│ ┃ renaming also updates groups and depends_on    KEY=VALUE, one per line",
		6:  "│ ┃ › api                                          │",
		29: "  tab  next  shift+tab  back  ctrl+s  save  esc  cancel",
	} {
		if !strings.HasPrefix(rows[i], want) {
			t.Errorf("row %d = %q, want prefix %q", i, rows[i], want)
		}
	}
	if !strings.HasSuffix(rows[0], " ctrl+s save ─╮") || !strings.HasPrefix(rows[28], "  EDIT API ") {
		t.Errorf("pane top %q, status bar %q", rows[0], rows[28])
	}
	if !strings.Contains(screenText(m), "│   [x] ● db  [ ] ● web") {
		t.Errorf("depends on:\n%s", screenText(m))
	}

	// Narrow: one column, Environment after Startup.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 46})
	text := screenText(m)
	if strings.Contains(launcherScreen(m)[2], "Environment") ||
		strings.Index(text, "Startup") > strings.Index(text, "Environment") {
		t.Errorf("not one column:\n%s", text)
	}
}

func TestProjectFormKeys(t *testing.T) {
	cfg := launcherConfig(t)
	m := openProjectForm(t, cfg, "", nil, 96, 30)
	f := m.form.form
	typeInto(m, "jobs")
	for range 3 {
		m.Update(key("tab")) // command, directory, depends on
	}
	if fd := f.focused(); fd.key != "depends_on" {
		t.Fatalf("focused %q", fd.key)
	}
	m.Update(key("right"))
	m.Update(key("space")) // db
	m.Update(key("space"))
	m.Update(key("space"))
	m.Update(key("shift+tab")) // directory
	m.Update(key("up"))        // ↑↓ don't move between fields
	m.Update(key("down"))
	if fd := f.focused(); fd.key != "dir" {
		t.Fatalf("focused %q after shift+tab, up, down", fd.key)
	}
	m.Update(key("shift+tab")) // command
	v := m.form.form.fields[0].str
	if *v != "jobs" {
		t.Fatalf("name = %q", *v)
	}

	// The color field cycles with ↑↓ and shows a swatch.
	for f.focused().key != "color" {
		m.Update(key("tab"))
	}
	m.Update(key("down"))
	m.Update(key("down"))
	if got := *f.focused().str; got != "red" {
		t.Fatalf("color after ↓↓ = %q", got)
	}
	m.Update(key("up"))
	m.Update(key("up"))
	m.Update(key("up"))
	if got := *f.focused().str; got != "bright-white" {
		t.Fatalf("color after ↑↑↑ = %q, want wrapping around", got)
	}
	if !strings.Contains(screenText(m), "┃ › ██ bright-white       ↑↓ cycle") {
		t.Errorf("color row:\n%s", screenText(m))
	}
	if help := launcherScreen(m)[29]; !strings.Contains(help, "↑↓  color") {
		t.Errorf("help = %q", help)
	}

	// enter in the environment is a new line; elsewhere it moves on.
	for f.focused().key != "env" {
		m.Update(key("shift+tab"))
	}
	typeInto(m, "A=1")
	m.Update(key("enter"))
	typeInto(m, "B=2")
	m.Update(key("enter"))
	m.Update(key("tab"))
	env := f.fields[slices.IndexFunc(f.fields, func(fd *formField) bool { return fd.key == "env" })]
	if fd := f.focused(); fd.key != "env_file" || *env.str != "A=1\nB=2\n" {
		t.Fatalf("focused %q, env %q", fd.key, *env.str)
	}
}

func TestProjectFormPaste(t *testing.T) {
	cfg := launcherConfig(t)
	m := openProjectForm(t, cfg, "", nil, 96, 30)
	f := m.form.form
	m.Update(tea.PasteMsg{Content: "jobs\r\n"}) // a copied line: its newline is dropped
	m.Update(key("tab"))
	m.Update(tea.PasteMsg{Content: "go run ."})
	if name, cmd := *f.fields[0].str, *f.fields[1].str; name != "jobs" || cmd != "go run ." {
		t.Fatalf("name %q, cmd %q", name, cmd)
	}
	for f.focused().key != "env" {
		m.Update(key("tab"))
	}
	m.Update(tea.PasteMsg{Content: "A=1\nB=2"}) // a text field keeps the lines
	if got := *f.focused().str; got != "A=1\nB=2" {
		t.Fatalf("env = %q", got)
	}
}

func TestProjectFormSaveKey(t *testing.T) {
	cfg := launcherConfig(t)
	var saved []*config.Config
	m := openProjectForm(t, cfg, "", nil, 96, 30)
	m.save = func(c *config.Config) error { saved = append(saved, c); return nil }
	f := m.form.form

	// Invalid: nothing saved, every error shown, the first one focused.
	typeInto(m, "jobs")
	m.Update(key("tab"))
	m.Update(key("tab"))
	m.Update(key("ctrl+s"))
	if m.form == nil || len(saved) != 0 {
		t.Fatalf("saved an invalid project: form open = %v", m.form != nil)
	}
	if fd := f.focused(); fd.key != "cmd" {
		t.Fatalf("focused %q, want the command", fd.key)
	}
	text := screenText(m)
	if !strings.Contains(text, "│ ┃ ✗ is required") || !strings.Contains(text, "NEW PROJECT  ✗ 1 error · command: is required") {
		t.Fatalf("errors not shown:\n%s", text)
	}

	// A bad regex shows under its field and in the status bar.
	for f.focused().key != "ready_log" {
		m.Update(key("tab"))
	}
	typeInto(m, "(")
	text = screenText(m)
	if !strings.Contains(text, "✗ invalid regex: missing closing ): `(`") || !strings.Contains(text, "✗ 2 errors · command: is required") {
		t.Fatalf("regex error:\n%s", text)
	}
	m.Update(key("backspace"))

	// Valid: saved from any field.
	for f.focused().key != "cmd" {
		m.Update(key("shift+tab"))
	}
	typeInto(m, "run")
	m.Update(key("tab"))
	m.Update(key("ctrl+s"))
	if m.form != nil || len(saved) != 1 || saved[0].Projects["jobs"].Cmd != "run" {
		t.Fatalf("not saved: form open = %v, saved %d", m.form != nil, len(saved))
	}
	if m.notice != `saved project "jobs"` {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestProjectFormErrorsAfterLeaving(t *testing.T) {
	m := openProjectForm(t, launcherConfig(t), "", nil, 96, 30)
	if strings.Contains(screenText(m), "✗") {
		t.Fatalf("errors before anything was typed:\n%s", screenText(m))
	}
	m.Update(key("tab")) // leaves the empty name
	if !strings.Contains(screenText(m), "✗ 1 error · name: is required") {
		t.Fatalf("no error after leaving the name:\n%s", screenText(m))
	}
}

func TestProjectFormButtons(t *testing.T) {
	cfg := launcherConfig(t)
	saves := 0
	m := openProjectForm(t, cfg, "db", nil, 96, 30)
	m.save = func(*config.Config) error { saves++; return nil }
	m.Update(key("shift+tab")) // wraps to Cancel
	if m.form.form.focus != m.form.form.cancelButton() {
		t.Fatalf("focus = %d", m.form.form.focus)
	}
	if !strings.Contains(screenText(m), "Save    Cancel") {
		t.Fatalf("buttons not in view:\n%s", screenText(m))
	}
	m.Update(key("left")) // Save
	m.Update(key("enter"))
	if m.form != nil || saves != 1 {
		t.Fatalf("enter on Save: form open = %v, saves = %d", m.form != nil, saves)
	}
	m.Update(key("e"))
	m.Update(key("shift+tab"))
	m.Update(key("enter")) // Cancel
	if m.form != nil || saves != 1 {
		t.Fatalf("enter on Cancel: form open = %v, saves = %d", m.form != nil, saves)
	}
}

func TestProjectFormDirCheck(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.env", []byte("A=1\nB=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := testGit()
	g.of["db"] = "/r/infra"
	st := newStyles(terminalTheme)
	info := func(dir, envFile string, git GitReader) (string, string) {
		pf := &projectForm{st: st, v: &projectValues{name: "db", dir: dir, envFile: envFile}}
		c := checkDir(git, "db", dir, envFile)
		pf.checked = &c
		return ansi.Strip(pf.dirInfo()), ansi.Strip(pf.envFileInfo())
	}
	for _, tt := range []struct {
		dir, envFile string
		git          GitReader
		dirInfo      string
		envInfo      string
	}{
		{dir, "", nil, "✓ exists", ""},
		{dir, ".env", g, "✓ exists · git: main", "✓ found · 2 variables"},
		{dir, "nope.env", nil, "✓ exists", "does not exist (starting the project fails)"},
		{dir + "/gone", ".env", nil, "does not exist yet (only a warning)", ""},
		{file, "", nil, "not a directory (only a warning)", ""},
	} {
		d, e := info(tt.dir, tt.envFile, tt.git)
		if d != tt.dirInfo || e != tt.envInfo {
			t.Errorf("check(%q, %q) = %q, %q; want %q, %q", tt.dir, tt.envFile, d, e, tt.dirInfo, tt.envInfo)
		}
	}
}

func TestProjectFormDirCheckDebounced(t *testing.T) {
	cfg := launcherConfig(t)
	m := openProjectForm(t, cfg, "db", nil, 96, 30)
	if !strings.Contains(screenText(m), "│   ✓ exists") {
		t.Fatalf("no check when opened:\n%s", screenText(m))
	}
	m.Update(key("tab"))
	m.Update(key("tab")) // directory
	_, cmd := m.Update(key("x"))
	if cmd == nil {
		t.Fatal("no check scheduled after typing in the directory")
	}
	// The tick of an older generation is ignored; the current one checks.
	form := projectFormOf(t, m)
	m.Update(dirCheckMsg{owner: form, gen: form.gen - 1})
	_, cmd = m.Update(dirCheckMsg{owner: form, gen: form.gen})
	runCmds(m, cmd)
	if !strings.Contains(screenText(m), "does not exist yet (only a warning)") {
		t.Fatalf("directory not checked again:\n%s", screenText(m))
	}
	// A late result of an older check doesn't replace it.
	m.Update(dirCheckedMsg{owner: form, gen: form.gen - 1})
	if !strings.Contains(screenText(m), "does not exist yet") {
		t.Fatal("older result replaced the newer one")
	}
}

// projectFormOf returns the state of the launcher's open project form.
func projectFormOf(t *testing.T, m *launcherModel) *projectForm {
	t.Helper()
	pf, ok := m.form.state.(*projectForm)
	if !ok {
		t.Fatal("no project form open")
	}
	return pf
}

func TestProjectFormPreview(t *testing.T) {
	cfg := launcherConfig(t)
	g := testGit()
	m := openProjectForm(t, cfg, "api", g, 96, 30)
	// preview returns the preview's two lines, from the selection bar on.
	preview := func() string {
		rows := launcherScreen(m)
		for i, r := range rows {
			if strings.Contains(r, "▌◐") {
				var lines []string
				for _, l := range rows[i : i+2] {
					l = strings.TrimSpace(l)
					lines = append(lines, strings.TrimSpace(strings.Trim(l[strings.Index(l, "▌"):], "│ ")))
				}
				return strings.Join(lines, "\n")
			}
		}
		return ""
	}
	if got := preview(); got != "▌◐ api\n▌  feat/login * ↓2" {
		t.Fatalf("preview = %q", got)
	}
	f := m.form.form
	for f.focused().key != "display_name" {
		m.Update(key("tab"))
	}
	typeInto(m, "API")
	if got := preview(); !strings.HasPrefix(got, "▌◐ API\n") {
		t.Fatalf("preview after typing a display name = %q", got)
	}
	m.Update(key("shift+tab"))
	typeInto(m, "magenta")
	if raw := m.render(); !strings.Contains(raw, "\x1b[1;35mAPI") {
		t.Errorf("preview not in the chosen color")
	}
}

func TestGroupForm(t *testing.T) {
	cfg := launcherConfig(t)
	var saved *config.Config
	m := newLauncherModel(cfg, func(c *config.Config) error { saved = c; return nil }, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 24})
	m.Update(key("g")) // on backend
	text := screenText(m)
	for _, want := range []string{"╭─ edit backend ─", "│ ┃ › backend", "│   [x] ● api  [x] ● db  [ ] ● web", "EDIT BACKEND", "2 projects"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	m.Update(key("tab"))
	m.Update(key("right"))
	m.Update(key("right"))
	m.Update(key("space")) // web
	m.Update(key("ctrl+s"))
	if saved == nil || !slices.Equal(saved.Groups["backend"], []string{"db", "api", "web"}) {
		t.Fatalf("saved %v", saved)
	}
}

func TestProjectFormPTY(t *testing.T) {
	cfg := launcherConfig(t)
	cfg.Projects["db"].PTY = true
	v := newProjectValues(cfg, "db")
	if !slices.Equal(v.pty, []string{ptyOption}) {
		t.Fatalf("pty = %v", v.pty)
	}
	v.pty = nil
	if c, err := v.candidate(cfg); err != nil || c.Projects["db"].PTY {
		t.Fatalf("after unticking: %+v, %v", c.Projects["db"], err)
	}

	m := openProjectForm(t, launcherConfig(t), "api", nil, 96, 30)
	f := m.form.form
	for f.focused().key != "pty" {
		m.Update(key("tab"))
	}
	m.Update(key("space"))
	if !strings.Contains(screenText(m), "[x] run on a pseudo-terminal") {
		t.Errorf("not ticked:\n%s", screenText(m))
	}
	if c, _, err := m.form.result(); err != nil || !c.Projects["api"].PTY {
		t.Errorf("result: %+v, %v", c.Projects["api"], err)
	}
}
