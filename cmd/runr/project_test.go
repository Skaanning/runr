package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"runr/internal/config"
)

func TestAdd(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()

	out := mustRun(t, "add", "db", "--dir", dir, "--ready-log", "ready", "--", "docker", "compose", "up")
	if !strings.Contains(out, `Added project "db"`) {
		t.Errorf("output: %q", out)
	}
	mustRun(t, "add", "api", "--dir", dir, "--env", "PORT=8080", "--env", "URL=http://x?a=b", "--env-file", ".env",
		"--depends-on", "db", "--color", "cyan", "--display-name", "API", "--", "go", "run", "./cmd/server")

	got := loadConfig(t).Projects
	want := map[string]*config.Project{
		"db": {Dir: dir, Cmd: "docker compose up", ReadyLog: "ready"},
		"api": {
			Dir: dir, Cmd: "go run ./cmd/server",
			Env:     map[string]string{"PORT": "8080", "URL": "http://x?a=b"},
			EnvFile: ".env", DependsOn: []string{"db"}, Color: "cyan", DisplayName: "API",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("projects = %+v\nwant %+v", got, want)
	}
}

func TestAddCommandForms(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()

	// A single quoted string, with or without --, is used verbatim.
	mustRun(t, "add", "a", "--dir", dir, "npm run dev -- --port 3000")
	mustRun(t, "add", "b", "--dir", dir, "--", "npm run dev")
	// Separate args after -- are re-quoted for the shell; flags after -- belong to the command.
	mustRun(t, "add", "c", "--dir", dir, "--", "sh", "-c", "echo 'hi there'", "--port", "3000")

	p := loadConfig(t).Projects
	if p["a"].Cmd != "npm run dev -- --port 3000" {
		t.Errorf("a: %q", p["a"].Cmd)
	}
	if p["b"].Cmd != "npm run dev" {
		t.Errorf("b: %q", p["b"].Cmd)
	}
	if runtime.GOOS != "windows" {
		if want := `sh -c 'echo '\''hi there'\''' --port 3000`; p["c"].Cmd != want {
			t.Errorf("c: %q, want %q", p["c"].Cmd, want)
		}
	}
}

func TestAddDirDefaults(t *testing.T) {
	testEnv(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	if err := os.Mkdir("sub", 0o755); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "add", "here", "--", "make", "run")
	mustRun(t, "add", "rel", "--dir", "sub", "--", "make", "run")
	mustRun(t, "add", "home", "--dir", "~/code/x", "--", "make", "run")

	p := loadConfig(t).Projects
	if p["here"].Dir != cwd {
		t.Errorf("here.Dir = %q, want %q", p["here"].Dir, cwd)
	}
	if want := filepath.Join(cwd, "sub"); p["rel"].Dir != want {
		t.Errorf("rel.Dir = %q, want %q", p["rel"].Dir, want)
	}
	if p["home"].Dir != "~/code/x" {
		t.Errorf("home.Dir = %q", p["home"].Dir)
	}
}

func TestAddErrors(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	mustRun(t, "add", "api", "--dir", dir, "--", "run")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"duplicate", []string{"add", "api", "--dir", dir, "--", "x"}, `project "api" already exists`},
		{"no command", []string{"add", "web", "--dir", dir}, "missing command"},
		{"empty after dash", []string{"add", "web", "--dir", dir, "--"}, "missing command after --"},
		{"too many args", []string{"add", "web", "npm", "run"}, "put the command after --"},
		{"two names", []string{"add", "web", "x", "--", "run"}, "exactly one project name"},
		{"bad env", []string{"add", "web", "--dir", dir, "--env", "PORT", "--", "x"}, `expected KEY=VALUE`},
		{"unknown dependency", []string{"add", "web", "--dir", dir, "--depends-on", "nope", "--", "x"}, `unknown project "nope"`},
		{"bad name", []string{"add", "my web", "--dir", dir, "--", "x"}, "invalid name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(t, tt.args...)
			wantErr(t, err, tt.want)
		})
	}

	// Failed adds must not have written anything.
	if names := sortedNames(loadConfig(t).Projects); !reflect.DeepEqual(names, []string{"api"}) {
		t.Errorf("projects after failed adds: %v", names)
	}
}

func TestAddWarnsAboutMissingDir(t *testing.T) {
	testEnv(t)
	missing := filepath.Join(t.TempDir(), "gone")
	_, stderr, err := run(t, "add", "api", "--dir", missing, "--", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "warning: project \"api\": dir: "+missing+" does not exist") {
		t.Errorf("stderr: %q", stderr)
	}
}

func TestEdit(t *testing.T) {
	testEnv(t)
	dir, other := t.TempDir(), t.TempDir()
	mustRun(t, "add", "db", "--dir", dir, "--", "run-db")
	mustRun(t, "add", "cache", "--dir", dir, "--", "run-cache")
	mustRun(t, "add", "api", "--dir", dir, "--env", "A=1", "--env", "B=2", "--depends-on", "db",
		"--color", "red", "--display-name", "API", "--", "old")

	out := mustRun(t, "edit", "api", "--dir", other, "--env", "B=3", "--env", "C=4", "--unset-env", "A",
		"--depends-on", "db,cache", "--color=", "--", "new", "cmd")
	if !strings.Contains(out, `Updated project "api"`) {
		t.Errorf("output: %q", out)
	}

	want := &config.Project{
		Dir: other, Cmd: "new cmd",
		Env:       map[string]string{"B": "3", "C": "4"},
		DependsOn: []string{"db", "cache"}, DisplayName: "API",
	}
	if got := loadConfig(t).Projects["api"]; !reflect.DeepEqual(got, want) {
		t.Errorf("api = %+v\nwant %+v", got, want)
	}

	// Clearing lists and removing the last env var.
	mustRun(t, "edit", "api", "--depends-on=", "--unset-env", "B", "--unset-env", "C")
	p := loadConfig(t).Projects["api"]
	if p.DependsOn != nil || p.Env != nil || p.Cmd != "new cmd" {
		t.Errorf("api after clearing = %+v", p)
	}
}

func TestEditErrors(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	mustRun(t, "add", "api", "--dir", dir, "--", "run")

	_, _, err := run(t, "edit", "nope", "--color", "red")
	wantErr(t, err, `unknown project "nope"`)

	_, _, err = run(t, "edit", "api")
	wantErr(t, err, "nothing to change")

	_, _, err = run(t, "edit", "api", "--depends-on", "api")
	wantErr(t, err, "cannot depend on itself")

	if p := loadConfig(t).Projects["api"]; p.DependsOn != nil {
		t.Errorf("failed edit was saved: %+v", p)
	}
}

func TestRm(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	mustRun(t, "add", "db", "--dir", dir, "--", "x")
	mustRun(t, "add", "api", "--dir", dir, "--depends-on", "db", "--", "x")
	mustRun(t, "add", "web", "--dir", dir, "--", "x")
	c := loadConfig(t)
	c.Groups["backend"] = []string{"db", "api"}
	c.Groups["all"] = []string{"db", "api", "web"}
	path, _ := config.Path()
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, "rm", "db")
	wantErr(t, err, `cannot remove "db": required by api`)

	_, _, err = run(t, "rm", "nope")
	wantErr(t, err, `unknown project "nope"`)

	out := mustRun(t, "rm", "db", "--force")
	for _, want := range []string{`Removed project "db"`, "removed from groups: all, backend", "removed from depends_on of: api"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	c = loadConfig(t)
	if c.Projects["db"] != nil || c.Projects["api"].DependsOn != nil {
		t.Errorf("projects after rm: %+v", c.Projects)
	}
	wantGroups := map[string][]string{"backend": {"api"}, "all": {"api", "web"}}
	if !reflect.DeepEqual(c.Groups, wantGroups) {
		t.Errorf("groups = %v, want %v", c.Groups, wantGroups)
	}
}

func TestLs(t *testing.T) {
	testEnv(t)

	out := mustRun(t, "ls")
	if !strings.Contains(out, "No projects registered yet") {
		t.Errorf("empty ls: %q", out)
	}

	dir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	mustRun(t, "add", "db", "--dir", dir, "--", "docker compose up")
	mustRun(t, "add", "api", "--dir", missing, "--depends-on", "db", "--", "go run .")
	c := loadConfig(t)
	c.Groups["backend"] = []string{"db", "api"}
	path, _ := config.Path()
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}

	out, stderr, err := run(t, "ls")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	wantPrefixes := []string{"PROJECT", "api ", "db ", "", "GROUP", "backend "}
	if len(lines) != len(wantPrefixes) {
		t.Fatalf("ls output:\n%s", out)
	}
	for i, prefix := range wantPrefixes {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], prefix)
		}
	}
	if !strings.Contains(lines[1], "go run .") || !strings.HasSuffix(lines[1], "db") {
		t.Errorf("api row: %q", lines[1])
	}
	if !strings.Contains(lines[5], "db, api") {
		t.Errorf("group row: %q", lines[5])
	}
	if !strings.Contains(stderr, `warning: project "api": dir:`) {
		t.Errorf("stderr: %q", stderr)
	}
}

func TestJoinCommand(t *testing.T) {
	tests := map[string]string{
		"plain":       quotePOSIX("plain"),
		"''":          quotePOSIX(""),
		"'a b'":       quotePOSIX("a b"),
		`'it'\''s'`:   quotePOSIX("it's"),
		"'$HOME'":     quotePOSIX("$HOME"),
		"./x=1,a@b":   quotePOSIX("./x=1,a@b"),
		`"a b"`:       quoteWindows("a b"),
		`"say \"x\""`: quoteWindows(`say "x"`),
		`""`:          quoteWindows(""),
		`"a&b"`:       quoteWindows("a&b"),
		`C:\x\y.exe`:  quoteWindows(`C:\x\y.exe`),
	}
	for want, got := range tests {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestDisplayPath(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	t.Setenv("USERPROFILE", "/home/me")
	tests := map[string]string{
		"/home/me":          "~",
		"/home/me/code/api": filepath.Join("~", "code/api"),
		"/home/meh/x":       "/home/meh/x",
		"/srv/api":          "/srv/api",
	}
	for in, want := range tests {
		if runtime.GOOS == "windows" {
			continue
		}
		if got := displayPath(in); got != want {
			t.Errorf("displayPath(%q) = %q, want %q", in, got, want)
		}
	}
}
