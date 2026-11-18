package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFormatOf(t *testing.T) {
	for path, want := range map[string]Format{
		"Procfile":                   Procfile,
		"/x/Procfile.dev":            Procfile,
		"compose.yaml":               Compose,
		"docker-compose.yml":         Compose,
		"/x/docker-compose.prod.yml": Compose,
		"compose.json":               "",
		"services.yaml":              "",
	} {
		got, ok := FormatOf(path)
		if got != want || ok != (want != "") {
			t.Errorf("FormatOf(%q) = %q, %v; want %q", path, got, ok, want)
		}
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	if got := Find(dir); got != nil {
		t.Fatalf("Find(empty) = %v", got)
	}
	write(t, dir, "docker-compose.yml", "")
	write(t, dir, "Procfile", "")
	os.Mkdir(filepath.Join(dir, "compose.yaml"), 0o755)
	if got, want := Find(dir), []string{"Procfile", "docker-compose.yml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Find = %v, want %v", got, want)
	}
}

func TestProcfile(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "Procfile", "# processes\r\n"+
		"web: bundle exec rails server -p $PORT\r\n"+
		"\n"+
		"  worker:bundle exec sidekiq -q a:b  \n"+
		"release_task: ./migrate.sh\n")
	f, err := Load(path, Procfile)
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{
		{Name: "web", Cmd: "bundle exec rails server -p $PORT"},
		{Name: "worker", Cmd: "bundle exec sidekiq -q a:b"},
		{Name: "release_task", Cmd: "./migrate.sh"},
	}
	if !reflect.DeepEqual(f.Services, want) {
		t.Fatalf("services = %+v\nwant %+v", f.Services, want)
	}
	if f.Dir != dir || f.Path != path || f.EnvFile != "" {
		t.Fatalf("dir = %q, path = %q, env file = %q", f.Dir, f.Path, f.EnvFile)
	}

	write(t, dir, ".env", "A=1\n")
	if f, _ = Load(path, Procfile); f.EnvFile != ".env" {
		t.Fatalf("env file = %q, want .env", f.EnvFile)
	}
}

func TestProcfileErrors(t *testing.T) {
	for content, want := range map[string]string{
		"web: a\nnot a process\n":  "Procfile: line 2: expected name: command",
		"web: a\nweb: b\n":         "line 2: web is defined twice",
		"web:   \n":                "line 1: web has no command",
		"# only a comment\n\n":     "Procfile: nothing to import",
		": nameless\n":             "line 1: expected name: command",
		"two words: a command\n":   "line 1: expected name: command",
		"web: a\n\tworker\t: b\n":  "line 2: expected name: command",
		"web: ok\nworker: also\n:": "line 3",
	} {
		_, err := Load(write(t, t.TempDir(), "Procfile", content), Procfile)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want containing %q", content, err, want)
		}
	}
}

const composeYAML = `
name: shop
x-common: &common
  restart: unless-stopped
services:
  db:
    <<: *common
    image: postgres:17
    ports:
      - "127.0.0.1:55432:5432"
  cache:
    image: redis
    ports:
      - 6379
      - target: 6379
        published: 16379
  api:
    build: ./api
    depends_on:
      - db
      - cache
    ports:
      - "8080:80/tcp"
  web:
    build: ./web
    depends_on:
      api:
        condition: service_healthy
        restart: true
    ports:
      - target: 3000
        published: "3000"
        protocol: tcp
  worker:
    build: ./api
    profiles: [jobs]
    depends_on:
      db: { condition: service_started }
      migrate: { condition: service_completed_successfully }
  migrate:
    build: ./api
    command: ./migrate
    ports: ["9999:9999"]
`

func TestCompose(t *testing.T) {
	dir := t.TempDir()
	f, err := Load(write(t, dir, "compose.yaml", composeYAML), Compose)
	if err != nil {
		t.Fatal(err)
	}
	up := func(svc string) []string {
		return []string{"docker", "compose", "up", "--no-deps", "--no-log-prefix", svc}
	}
	stop := func(svc string) []string { return []string{"docker", "compose", "stop", svc} }
	want := []Service{
		{Name: "db", Args: up("db"), StopArgs: stop("db"), Port: 55432},
		{Name: "cache", Args: up("cache"), StopArgs: stop("cache"), Port: 16379},
		{Name: "api", Args: up("api"), StopArgs: stop("api"), DependsOn: []string{"db", "cache"}, Port: 8080},
		{Name: "web", Args: up("web"), StopArgs: stop("web"), DependsOn: []string{"api"}, Port: 3000},
		{Name: "worker", Args: up("worker"), StopArgs: stop("worker"), DependsOn: []string{"db", "migrate"}},
		{Name: "migrate", Args: up("migrate"), StopArgs: stop("migrate"), OneShot: true}, // its port is dropped
	}
	if !reflect.DeepEqual(f.Services, want) {
		t.Fatalf("services =\n%+v\nwant\n%+v", f.Services, want)
	}
	if f.Name != "shop" || f.Dir != dir {
		t.Fatalf("name = %q, dir = %q", f.Name, f.Dir)
	}
}

func TestComposeFileArg(t *testing.T) {
	dir := t.TempDir()
	content := "services:\n  app:\n    image: x\n"
	args := func(name string) []string {
		t.Helper()
		f, err := Load(filepath.Join(dir, name), Compose)
		if err != nil {
			t.Fatal(err)
		}
		return f.Services[0].Args
	}

	write(t, dir, "docker-compose.yml", content)
	if got := args("docker-compose.yml"); strings.Contains(strings.Join(got, " "), "-f") {
		t.Fatalf("only compose file: args = %v, want no -f", got)
	}
	// docker compose would pick compose.yaml in this directory.
	write(t, dir, "compose.yaml", content)
	if got := strings.Join(args("docker-compose.yml"), " "); !strings.HasPrefix(got, "docker compose -f docker-compose.yml up") {
		t.Fatalf("shadowed default name: args = %v", got)
	}
	if got := strings.Join(args("compose.yaml"), " "); strings.Contains(got, "-f") {
		t.Fatalf("compose.yaml: args = %v", got)
	}
	write(t, dir, "compose.dev.yaml", content)
	if got := strings.Join(args("compose.dev.yaml"), " "); got != "docker compose -f compose.dev.yaml up --no-deps --no-log-prefix app" {
		t.Fatalf("other name: args = %v", got)
	}
}

func TestComposePorts(t *testing.T) {
	for ports, want := range map[string]int{
		`["3000"]`:                          0, // random host port
		`[3000]`:                            0,
		`["3000:3000"]`:                     3000,
		`["8000-8010:80"]`:                  8000,
		`["127.0.0.1::80"]`:                 0,
		`["[::1]:9000:90"]`:                 9000,
		`["53:53/udp", "5300:53"]`:          5300,
		`["${PORT:-3000}:3000", "4000:40"]`: 4000,
		`[{target: 53, published: 53, protocol: udp}]`: 0,
		`[{target: 80, published: "8000-8005"}]`:       8000,
		`[{target: 80}]`:                               0,
	} {
		yml := "services:\n  s:\n    ports: " + ports + "\n"
		_, services, err := parseCompose([]byte(yml), "")
		if err != nil {
			t.Errorf("%s: %v", ports, err)
			continue
		}
		if got := services[0].Port; got != want {
			t.Errorf("ports %s: port = %d, want %d", ports, got, want)
		}
	}
}

func TestComposeErrors(t *testing.T) {
	for yml, want := range map[string]string{
		"services:\n  api:\n    depends_on: [db]\n": "service api depends on db, which isn't in the file",
		"services: [a, b]\n":                        "services: expected a mapping",
		"services:\n  api:\n    depends_on: db\n":   "depends_on: expected a list or a mapping",
		"services:\n  a: {}\n  a: {}\n":             "line 3: service a is defined twice",
		"name: empty\n":                             "nothing to import",
		"services:\n  a: [\n":                       "compose.yaml: yaml:",
	} {
		_, err := Load(write(t, t.TempDir(), "compose.yaml", yml), Compose)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want containing %q", yml, err, want)
		}
	}
}
