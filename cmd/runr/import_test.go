package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/config"
)

// fixture returns the absolute path of a file in testdata/import.
func fixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "import", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportCompose(t *testing.T) {
	testEnv(t)
	path := fixture(t, "compose.yaml")
	out := mustRun(t, "import", path)
	for _, want := range []string{
		"Imported 3 projects from ",
		"db       docker compose up --no-deps --no-log-prefix db               port 47811",
		"api      docker compose up --no-deps --no-log-prefix api  db          port 47812",
		"web      docker compose up --no-deps --no-log-prefix web  api",
		`Created group "shop"; start it with: runr up shop`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	c := loadConfig(t)
	dir := filepath.Dir(path)
	want := map[string]*config.Project{
		"db":  {Dir: dir, Cmd: "docker compose up --no-deps --no-log-prefix db", ReadyPort: 47811},
		"api": {Dir: dir, Cmd: "docker compose up --no-deps --no-log-prefix api", DependsOn: []string{"db"}, ReadyPort: 47812},
		"web": {Dir: dir, Cmd: "docker compose up --no-deps --no-log-prefix web", DependsOn: []string{"api"}},
	}
	if !reflect.DeepEqual(c.Projects, want) {
		t.Fatalf("projects = %+v", c.Projects)
	}
	if got := c.Groups["shop"]; !reflect.DeepEqual(got, []string{"db", "api", "web"}) {
		t.Fatalf("group shop = %v", got)
	}
}

func TestImportProcfile(t *testing.T) {
	testEnv(t)
	path := fixture(t, "Procfile")
	mustRun(t, "import", path, "--prefix", "app.", "--group", "procs")

	c := loadConfig(t)
	dir := filepath.Dir(path)
	want := map[string]*config.Project{
		"app.web":    {Dir: dir, Cmd: "echo web up"},
		"app.worker": {Dir: dir, Cmd: "echo worker up"},
	}
	if !reflect.DeepEqual(c.Projects, want) {
		t.Fatalf("projects = %+v", c.Projects)
	}
	if !reflect.DeepEqual(c.Groups, map[string][]string{"procs": {"app.web", "app.worker"}}) {
		t.Fatalf("groups = %v", c.Groups)
	}
}

func TestImportClashes(t *testing.T) {
	testEnv(t)
	mustRun(t, "add", "api", "--", "true")
	mustRun(t, "add", "other", "--", "true")
	mustRun(t, "group", "create", "web", "other")
	path := fixture(t, "compose.yaml")

	_, _, err := run(t, "import", path)
	wantErr(t, err, "nothing imported: these names are taken: api (a project), web (a group)\nuse --prefix to import them under other names, e.g. --prefix shop-")
	if c := loadConfig(t); len(c.Projects) != 2 || len(c.Groups) != 1 {
		t.Fatalf("config changed: %+v %v", c.Projects, c.Groups)
	}

	mustRun(t, "group", "create", "shop", "other")
	_, _, err = run(t, "import", path, "--prefix", "shop-")
	wantErr(t, err, `the group name "shop" is taken by a group`)

	mustRun(t, "import", path, "--prefix", "shop-", "--no-group")
	c := loadConfig(t)
	if got := c.Projects["shop-api"]; got == nil || !reflect.DeepEqual(got.DependsOn, []string{"shop-db"}) {
		t.Fatalf("shop-api = %+v", got)
	}
	if len(c.Groups) != 2 {
		t.Fatalf("groups = %v, want no new group", c.Groups)
	}

	// A group named like one of the imported projects.
	_, _, err = run(t, "import", fixture(t, "Procfile"), "--group", "worker")
	wantErr(t, err, `the group name "worker" is taken by an imported project`)
}

func TestImportDryRun(t *testing.T) {
	path := testEnv(t)
	out := mustRun(t, "import", fixture(t, "compose.yaml"), "--dry-run")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config written by --dry-run: %v", err)
	}
	for _, want := range []string{
		"# runr import --dry-run: would add to ",
		"[projects.api]\n",
		"cmd = 'docker compose up --no-deps --no-log-prefix api'\ndepends_on = ['db']\nready_port = 47812\n",
		"[groups]\nshop = ['db', 'api', 'web']\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The printed TOML is a valid config.
	file := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(file, []byte(out), 0o600)
	if c, err := config.Load(file); err != nil || len(c.Projects) != 3 {
		t.Fatalf("Load(dry-run output) = %v, %v", c, err)
	}
}

func TestImportFindsFile(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)

	_, _, err := run(t, "import")
	wantErr(t, err, "no Procfile or compose file in the current directory")

	os.WriteFile("Procfile", []byte("web: echo hi\n"), 0o644)
	os.WriteFile("docker-compose.yml", []byte("services:\n  db:\n    image: x\n"), 0o644)
	_, _, err = run(t, "import")
	wantErr(t, err, "found several files to import in the current directory: Procfile, docker-compose.yml\nname the one to import, e.g. runr import Procfile")

	// --format narrows the search; the group is named after the directory.
	mustRun(t, "import", "--format", "compose", "--dry-run")
	out := mustRun(t, "import", dir, "--format", "procfile")
	if !strings.Contains(out, "Created group "+`"`+filepath.Base(dir)) {
		t.Fatalf("output: %s", out)
	}
	if c := loadConfig(t); c.Projects["web"] == nil || c.Projects["web"].Dir != dir {
		t.Fatalf("projects = %+v", c.Projects)
	}
}

func TestImportArguments(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	odd := filepath.Join(dir, "services.yaml")
	os.WriteFile(odd, []byte("services:\n  my_db!:\n    image: x\n"), 0o644)

	_, _, err := run(t, "import", odd)
	wantErr(t, err, "can't tell the format of "+odd+" from its name; pass --format")
	_, _, err = run(t, "import", odd, "--format", "yaml")
	wantErr(t, err, `--format "yaml": use procfile or compose`)
	if _, _, err = run(t, "import", filepath.Join(dir, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: err = %v", err)
	}
	_, _, err = run(t, "import", odd, "--group", "g", "--no-group")
	wantErr(t, err, "none of the others can be")

	// Invalid characters are replaced; -f names the file.
	mustRun(t, "import", odd, "--format", "compose", "--group", "g")
	p := loadConfig(t).Projects["my_db-"]
	if p == nil || p.Cmd != "docker compose -f services.yaml up --no-deps --no-log-prefix 'my_db!'" && p.Cmd != `docker compose -f services.yaml up --no-deps --no-log-prefix my_db!` {
		t.Fatalf("project = %+v", p)
	}
}
