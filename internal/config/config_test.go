package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const specExample = `
[settings]
shutdown_timeout = "5s"
scrollback_lines = 10000

[projects.db]
dir = "~/code/infra"
cmd = "docker compose up postgres"
ready_log = "ready to accept connections"

[projects.api]
dir = "~/code/api"
cmd = "go run ./cmd/server"
env = { PORT = "8080" }
env_file = ".env"
depends_on = ["db"]
color = "cyan"
display_name = "API"

[projects.web]
dir = "~/code/web"
cmd = "npm run dev"
ready_url = "http://localhost:5173/"

[projects.cache]
dir = "~/code/infra"
cmd = "redis-server"
ready_port = 6379

[projects.migrate]
dir = "~/code/api"
cmd = "go run ./cmd/migrate"
ready = "exit"

[groups]
backend = ["db", "api", "auth", "worker"]
`

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSpecExample(t *testing.T) {
	c, err := Load(writeFile(t, specExample))
	if err != nil {
		t.Fatal(err)
	}

	if got := c.Settings.ShutdownTimeout(); got != 5*time.Second {
		t.Errorf("ShutdownTimeout = %v", got)
	}
	if got := c.Settings.ScrollbackLines(); got != 10000 {
		t.Errorf("ScrollbackLines = %v", got)
	}

	want := &Project{
		Dir:         "~/code/api",
		Cmd:         "go run ./cmd/server",
		Env:         map[string]string{"PORT": "8080"},
		EnvFile:     ".env",
		DependsOn:   []string{"db"},
		Color:       "cyan",
		DisplayName: "API",
	}
	if got := c.Projects["api"]; !reflect.DeepEqual(got, want) {
		t.Errorf("api = %+v, want %+v", got, want)
	}
	if got := c.Projects["db"].ReadyLog; got != "ready to accept connections" {
		t.Errorf("db.ReadyLog = %q", got)
	}
	if c.Projects["web"].ReadyURL != "http://localhost:5173/" || c.Projects["cache"].ReadyPort != 6379 {
		t.Errorf("web = %+v, cache = %+v", c.Projects["web"], c.Projects["cache"])
	}
	if !c.Projects["migrate"].OneShot() {
		t.Errorf("migrate.Ready = %q, want one-shot", c.Projects["migrate"].Ready)
	}
	if got := c.Groups["backend"]; !reflect.DeepEqual(got, []string{"db", "api", "auth", "worker"}) {
		t.Errorf("backend = %v", got)
	}
}

func TestRoundTrip(t *testing.T) {
	c, err := Load(writeFile(t, specExample))
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "out.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, c2) {
		t.Errorf("round trip mismatch:\n%+v\n%+v", c, c2)
	}
}

func TestSaveIsStable(t *testing.T) {
	c, err := Load(writeFile(t, specExample))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.toml"), filepath.Join(dir, "b.toml")
	if err := Save(a, c); err != nil {
		t.Fatal(err)
	}
	if err := Save(b, c); err != nil {
		t.Fatal(err)
	}
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if string(da) != string(db) {
		t.Errorf("output differs between saves:\n%s\n---\n%s", da, db)
	}
	if !strings.Contains(string(da), "env = {PORT = '8080'}") {
		t.Errorf("env not written as inline table:\n%s", da)
	}
	if strings.Index(string(da), "[projects.api]") > strings.Index(string(da), "[projects.db]") {
		t.Errorf("projects not sorted:\n%s", da)
	}
}

func TestLoadMissingFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Projects == nil || c.Groups == nil || len(c.Projects) != 0 {
		t.Errorf("expected empty initialized config, got %+v", c)
	}
	if c.Settings.ShutdownTimeout() != DefaultShutdownTimeout || c.Settings.ScrollbackLines() != DefaultScrollbackLines ||
		c.Settings.ErrorPattern() != DefaultErrorPattern {
		t.Errorf("defaults not applied: %+v", c.Settings)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"unknown field", "[projects.api]\ndir = \"x\"\ncmdd = \"y\"\n", "cmdd"},
		{"bad duration", "[settings]\nshutdown_timeout = \"soon\"\n", "invalid duration"},
		{"syntax error", "[projects.api\n", "config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeFile(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSaveCreatesDirsAndLeavesNoTempFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "runr")
	path := filepath.Join(dir, "config.toml")

	c := New()
	c.Projects["api"] = &Project{Dir: "/srv/api", Cmd: "make run"}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	// Overwrite an existing file too.
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Errorf("unexpected files in config dir: %v", entries)
	}
}

func TestSaveEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, New()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.TrimSpace(string(data)) != "" {
		t.Errorf("expected empty file, got %q", data)
	}
}

func TestPath(t *testing.T) {
	t.Setenv(EnvVar, "/custom/runr.toml")
	if p, err := Path(); err != nil || p != "/custom/runr.toml" {
		t.Errorf("Path() = %q, %v", p, err)
	}

	t.Setenv(EnvVar, "")
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join("runr", "config.toml")) {
		t.Errorf("Path() = %q", p)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	tests := map[string]string{
		"~":          home,
		"~/code/api": filepath.Join(home, "code", "api"),
		"/abs/path":  "/abs/path",
		"rel/path":   "rel/path",
		"~user/x":    "~user/x",
	}
	for in, want := range tests {
		if got, err := ExpandHome(in); err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestResolvedEnvFile(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "secrets.env")
	tests := []struct {
		name string
		p    Project
		want string
	}{
		{"none", Project{Dir: "/srv/api"}, ""},
		{"relative", Project{Dir: "/srv/api", EnvFile: ".env"}, filepath.Join("/srv/api", ".env")},
		{"absolute", Project{Dir: "/srv/api", EnvFile: abs}, abs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.p.ResolvedEnvFile()
			if err != nil || got != tt.want {
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	if got := (&Project{}).Label("api"); got != "api" {
		t.Errorf("Label = %q", got)
	}
	if got := (&Project{DisplayName: "API"}).Label("api"); got != "API" {
		t.Errorf("Label = %q", got)
	}
}

func TestClone(t *testing.T) {
	c := &Config{
		Projects: map[string]*Project{"api": {Dir: "/x", Cmd: "run", Env: map[string]string{"A": "1"}, DependsOn: []string{"db"}}, "db": {Dir: "/x", Cmd: "db"}},
		Groups:   map[string][]string{"backend": {"db", "api"}},
	}
	cp := c.Clone()
	cp.Projects["api"].Env["A"] = "2"
	cp.Projects["api"].DependsOn[0] = "x"
	cp.Groups["backend"][0] = "x"
	cp.Projects["api"].Cmd = "other"
	if c.Projects["api"].Env["A"] != "1" || c.Projects["api"].DependsOn[0] != "db" || c.Groups["backend"][0] != "db" || c.Projects["api"].Cmd != "run" {
		t.Fatal("clone shares data with the original")
	}
}

func TestRemoveProject(t *testing.T) {
	c := &Config{
		Projects: map[string]*Project{
			"db":  {Cmd: "db"},
			"api": {Cmd: "api", DependsOn: []string{"db", "cache"}},
			"web": {Cmd: "web", DependsOn: []string{"db"}},
		},
		Groups: map[string][]string{"all": {"db", "api", "web"}, "front": {"web"}, "data": {"db"}},
	}
	groups, dependents := c.RemoveProject("db")
	if strings.Join(groups, ",") != "all,data" || strings.Join(dependents, ",") != "api,web" {
		t.Fatalf("groups = %v, dependents = %v", groups, dependents)
	}
	if c.Projects["db"] != nil || strings.Join(c.Projects["api"].DependsOn, ",") != "cache" ||
		len(c.Projects["web"].DependsOn) != 0 || strings.Join(c.Groups["all"], ",") != "api,web" || len(c.Groups["data"]) != 0 {
		t.Fatalf("config after removal: %+v %v", c.Projects, c.Groups)
	}
}

func TestShowAll(t *testing.T) {
	if !(Settings{}).ShowAll() {
		t.Error("ShowAll defaults to false")
	}
	c, err := Load(writeFile(t, "[settings]\nshow_all = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.ShowAll() {
		t.Error("show_all = false not applied")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(path); err != nil || c.Settings.ShowAll() {
		t.Errorf("show_all = false lost on save (err %v)", err)
	}
}

func TestColorOf(t *testing.T) {
	c := &Config{Projects: map[string]*Project{
		"web": {}, "api": {Color: "#ff8800"}, "db": {}, "auth": {},
	}}
	// Projects without a color get the default colors in name order: auth, db, web.
	for name, want := range map[string]string{"api": "#ff8800", "auth": "cyan", "db": "magenta", "web": "yellow"} {
		if got := c.ColorOf(name); got != want {
			t.Errorf("ColorOf(%s) = %q, want %q", name, got, want)
		}
	}
	for i := range len(DefaultColors) + 1 {
		c.Projects[fmt.Sprintf("x%02d", i)] = &Project{}
	}
	if got := c.ColorOf("x10"); got != DefaultColors[(3+10)%len(DefaultColors)] {
		t.Errorf("ColorOf(x10) = %q, want the colors to repeat", got)
	}
}

func TestThemeBackground(t *testing.T) {
	if (Settings{}).ThemeBackground() {
		t.Error("ThemeBackground defaults to true")
	}
	c, err := Load(writeFile(t, "[settings]\ntheme_background = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Settings.ThemeBackground() {
		t.Error("theme_background = true not applied")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(path); err != nil || !c.Settings.ThemeBackground() {
		t.Errorf("theme_background = true lost on save (err %v)", err)
	}
}

func TestTheme(t *testing.T) {
	if got := (Settings{}).Theme(); got != "terminal" {
		t.Errorf("default theme = %q", got)
	}
	c, err := Load(writeFile(t, "[settings]\ntheme = \"nord\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Settings.Theme(); got != "nord" {
		t.Errorf("theme = %q", got)
	}
	if _, err := c.Validate(); err != nil {
		t.Error(err)
	}
}

// setHome points the user's home directory at dir for the test.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestRoot(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	abs := t.TempDir()
	tests := []struct {
		value   string
		want    string
		ok      bool
		wantErr string
	}{
		{"", "", false, ""},
		{"  ", "", false, ""},
		{abs, abs, true, ""},
		{abs + string(filepath.Separator), abs, true, ""},
		{"~/work", filepath.Join(home, "work"), true, ""},
		{"work", "", false, "RUNR_ROOT=work is not an absolute path"},
	}
	for _, tt := range tests {
		t.Setenv(RootEnvVar, tt.value)
		got, ok, err := Root()
		if tt.wantErr != "" {
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("Root() with %q: err = %v, want %q", tt.value, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want || ok != tt.ok {
			t.Errorf("Root() with %q = %q, %v, %v; want %q, %v", tt.value, got, ok, err, tt.want, tt.ok)
		}
	}
}

func TestExpandDir(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := t.TempDir()
	t.Setenv(RootEnvVar, root)
	tests := map[string]string{
		"$RUNR_ROOT":           root,
		"$RUNR_ROOT/":          root,
		"$RUNR_ROOT/api":       filepath.Join(root, "api"),
		"$RUNR_ROOT/apps/web":  filepath.Join(root, "apps", "web"),
		`$RUNR_ROOT\apps\web`:  filepath.Join(root, "apps", "web"),
		"$RUNR_ROOTX/api":      "$RUNR_ROOTX/api",
		"${RUNR_ROOT}/api":     "${RUNR_ROOT}/api",
		"~/code":               filepath.Join(home, "code"),
		"/srv/api":             "/srv/api",
		"rel/$RUNR_ROOT/thing": "rel/$RUNR_ROOT/thing",
	}
	for in, want := range tests {
		if got, err := ExpandDir(in); err != nil || got != want {
			t.Errorf("ExpandDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	t.Setenv(RootEnvVar, "~/work")
	if got, err := ExpandDir("$RUNR_ROOT/api"); err != nil || got != filepath.Join(home, "work", "api") {
		t.Errorf("with a ~ root: %q, %v", got, err)
	}

	t.Setenv(RootEnvVar, "")
	if _, err := ExpandDir("$RUNR_ROOT/api"); err == nil || err.Error() != "$RUNR_ROOT is not set" {
		t.Errorf("unset root: err = %v", err)
	}
	if got, err := ExpandDir("/srv/api"); err != nil || got != "/srv/api" {
		t.Errorf("unset root, plain dir: %q, %v", got, err)
	}

	t.Setenv(RootEnvVar, "relative")
	if _, err := ExpandDir("$RUNR_ROOT/api"); err == nil || !strings.Contains(err.Error(), "not an absolute path") {
		t.Errorf("relative root: err = %v", err)
	}
}

func TestNormalizeDir(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "work")
	cwd := t.TempDir()
	t.Chdir(cwd)

	check := func(in, want string) {
		t.Helper()
		if got, err := NormalizeDir(in); err != nil || got != want {
			t.Errorf("NormalizeDir(%q) with RUNR_ROOT=%q = %q, %v; want %q", in, os.Getenv(RootEnvVar), got, err, want)
		}
	}

	// Without a root: as before.
	t.Setenv(RootEnvVar, "")
	check("", "")
	check("~/work/api/", "~/work/api")
	check("sub", filepath.Join(cwd, "sub"))
	check(filepath.Join(root, "api"), filepath.Join(root, "api"))
	check("$RUNR_ROOT/api/", "$RUNR_ROOT/api") // kept as written, even unset

	for _, value := range []string{root, "~/work"} {
		t.Setenv(RootEnvVar, value)
		check(filepath.Join(root, "api"), "$RUNR_ROOT/api")
		check(filepath.Join(root, "apps", "web")+string(filepath.Separator), "$RUNR_ROOT/apps/web")
		check(root, "$RUNR_ROOT")
		check("~/work/api", "$RUNR_ROOT/api")
		check("~/work", "$RUNR_ROOT")
		check("$RUNR_ROOT/api", "$RUNR_ROOT/api")
		check(`$RUNR_ROOT\api`, `$RUNR_ROOT\api`)
		check(root+"2", root+"2")                                               // a sibling with the same prefix
		check(filepath.Join(root, "..", "other"), filepath.Join(home, "other")) // outside
		check("~/other", "~/other")
		check("sub", filepath.Join(cwd, "sub"))
	}

	// A relative root is ignored here; ExpandDir and Validate report it.
	t.Setenv(RootEnvVar, "work")
	check(filepath.Join(root, "api"), filepath.Join(root, "api"))
}
