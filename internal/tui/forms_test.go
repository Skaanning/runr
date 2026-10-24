package tui

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

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
	if d := c.Projects["x"].Dir; !strings.HasPrefix(d, "/") || !strings.HasSuffix(d, "relative/dir") {
		t.Fatalf("dir = %q", d)
	}
	v.dir = "~/code"
	if c, _ := v.candidate(cfg); c.Projects["x"].Dir != "~/code" {
		t.Fatalf("dir = %q", c.Projects["x"].Dir)
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

// fakeForm returns a form whose result is c, already completed.
func fakeForm(c *config.Config, focus launcherItem, save bool) *editForm {
	f := &editForm{
		title:   "test",
		result:  func() (*config.Config, launcherItem, error) { return c, focus, nil },
		rebuild: func() *huh.Form { return newForm(huh.NewInput()) },
		save:    &save,
	}
	f.form = f.rebuild()
	return f
}

func submit(m *launcherModel, f *editForm) {
	m.openForm(f)
	f.form.State = huh.StateCompleted
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
}

func TestLauncherFormSaves(t *testing.T) {
	cfg := launcherConfig(t)
	var saved []*config.Config
	m := newLauncherModel(cfg, func(c *config.Config) error { saved = append(saved, c); return nil }, newStyles(terminalPalette))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	shared := cfg // e.g. the app's pointer

	c := cfg.Clone()
	c.Projects["cron"] = &config.Project{Dir: cfg.Projects["db"].Dir, Cmd: "cron"}
	submit(m, fakeForm(c, launcherItem{name: "cron"}, true))

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
	m := newLauncherModel(cfg, func(*config.Config) error { calls++; return saveErr }, newStyles(terminalPalette))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})

	submit(m, fakeForm(cfg.Clone(), launcherItem{}, false)) // Cancel chosen
	if m.form != nil || calls != 0 {
		t.Fatalf("cancel: form open = %v, calls = %d", m.form != nil, calls)
	}

	invalid := cfg.Clone()
	invalid.Projects["bad"] = &config.Project{Dir: "/x"} // no cmd
	submit(m, fakeForm(invalid, launcherItem{name: "bad"}, true))
	if m.form == nil || calls != 0 || !strings.Contains(m.notice, "cmd: is required") {
		t.Fatalf("invalid: form open = %v, calls = %d, notice = %q", m.form != nil, calls, m.notice)
	}

	m.form = nil
	submit(m, fakeForm(cfg.Clone(), launcherItem{name: "db"}, true))
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
	for k, want := range map[string]string{"n": "new project", "g": "edit group backend", "e": "edit group backend"} {
		m.Update(key(k))
		if m.form == nil || m.form.title != want {
			t.Fatalf("%s: form = %+v", k, m.form)
		}
		if !strings.HasPrefix(launcherScreen(m)[0], " runr · "+want) {
			t.Fatalf("%s: header = %q", k, launcherScreen(m)[0])
		}
		m.Update(key("esc"))
	}
	for range 3 {
		m.Update(key("down")) // db
	}
	m.Update(key("space"))
	m.Update(key("e"))
	if m.form.title != "edit project db" {
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
	m := newLauncherModel(cfg, func(*config.Config) error { saves++; return nil }, newStyles(terminalPalette))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(key("d")) // backend
	if !strings.Contains(strings.Join(launcherScreen(m), "\n"), `Delete group "backend"? Its projects are kept.`) {
		t.Fatalf("screen:\n%s", strings.Join(launcherScreen(m), "\n"))
	}
	if help := launcherScreen(m)[19]; help != " y delete  n cancel" {
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
	m := newLauncherModel(cfg, nil, newStyles(terminalPalette))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.selected["db"] = true

	c := cfg.Clone()
	if _, _, err := c.Rename("db", "postgres"); err != nil {
		t.Fatal(err)
	}
	f := fakeForm(c, launcherItem{name: "postgres"}, true)
	f.original = launcherItem{name: "db"}
	submit(m, f)

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
