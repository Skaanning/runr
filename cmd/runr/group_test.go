package main

import (
	"reflect"
	"strings"
	"testing"
)

// setupProjects registers projects with no dependencies.
func setupProjects(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		mustRun(t, "add", name, "--dir", dir, "--", "run")
	}
}

func TestGroupCreate(t *testing.T) {
	testEnv(t)
	setupProjects(t, "db", "api", "auth")

	out := mustRun(t, "group", "create", "backend", "db", "api", "db", "auth")
	if want := `Created group "backend": db, api, auth`; !strings.Contains(out, want) {
		t.Errorf("output %q, want %q", out, want)
	}
	if got := loadConfig(t).Groups["backend"]; !reflect.DeepEqual(got, []string{"db", "api", "auth"}) {
		t.Errorf("backend = %v", got)
	}
}

func TestGroupCreateErrors(t *testing.T) {
	testEnv(t)
	setupProjects(t, "db", "api")
	mustRun(t, "group", "create", "backend", "db")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"exists", []string{"group", "create", "backend", "api"}, `group "backend" already exists`},
		{"unknown project", []string{"group", "create", "web", "nope"}, `unknown project "nope"`},
		{"clashes with project", []string{"group", "create", "api", "db"}, "project and group names must be unique"},
		{"nested", []string{"group", "create", "all", "backend"}, "groups can only contain projects"},
		{"no members", []string{"group", "create", "web"}, "requires at least 2 arg(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(t, tt.args...)
			wantErr(t, err, tt.want)
		})
	}

	if got := sortedNames(loadConfig(t).Groups); !reflect.DeepEqual(got, []string{"backend"}) {
		t.Errorf("groups after failed creates: %v", got)
	}
}

func TestGroupAdd(t *testing.T) {
	testEnv(t)
	setupProjects(t, "db", "api", "auth")
	mustRun(t, "group", "create", "backend", "db")

	out := mustRun(t, "group", "add", "backend", "api", "db", "auth")
	for _, want := range []string{`Added api, auth to group "backend"`, "already in group: db"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if got := loadConfig(t).Groups["backend"]; !reflect.DeepEqual(got, []string{"db", "api", "auth"}) {
		t.Errorf("backend = %v", got)
	}

	out = mustRun(t, "group", "add", "backend", "api")
	if !strings.Contains(out, `Group "backend" already contains api`) {
		t.Errorf("output: %q", out)
	}

	_, _, err := run(t, "group", "add", "nope", "api")
	wantErr(t, err, `unknown group "nope"`)
	_, _, err = run(t, "group", "add", "backend", "ghost")
	wantErr(t, err, `unknown project "ghost"`)
}

func TestGroupRm(t *testing.T) {
	testEnv(t)
	setupProjects(t, "db", "api", "auth")
	mustRun(t, "group", "create", "backend", "db", "api", "auth")

	out := mustRun(t, "group", "rm", "backend", "db", "auth")
	if !strings.Contains(out, `Removed db, auth from group "backend"`) {
		t.Errorf("output: %q", out)
	}
	if got := loadConfig(t).Groups["backend"]; !reflect.DeepEqual(got, []string{"api"}) {
		t.Errorf("backend = %v", got)
	}

	_, _, err := run(t, "group", "rm", "backend", "db")
	wantErr(t, err, `"db" is not in group "backend"`)
	_, _, err = run(t, "group", "rm", "nope", "db")
	wantErr(t, err, `unknown group "nope"`)

	// Removing the last member keeps the group but warns.
	_, stderr, err := run(t, "group", "rm", "backend", "api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, `warning: group "backend": group is empty`) {
		t.Errorf("stderr: %q", stderr)
	}
	groups := loadConfig(t).Groups
	if members, ok := groups["backend"]; !ok || len(members) != 0 {
		t.Errorf("groups = %v", groups)
	}
}

func TestGroupDelete(t *testing.T) {
	testEnv(t)
	setupProjects(t, "db")
	mustRun(t, "group", "create", "backend", "db")

	out := mustRun(t, "group", "delete", "backend")
	if !strings.Contains(out, `Deleted group "backend"`) {
		t.Errorf("output: %q", out)
	}
	c := loadConfig(t)
	if len(c.Groups) != 0 || c.Projects["db"] == nil {
		t.Errorf("after delete: groups %v, projects %v", c.Groups, c.Projects)
	}

	_, _, err := run(t, "group", "delete", "backend")
	wantErr(t, err, `unknown group "backend"`)
}

func TestGroupLs(t *testing.T) {
	testEnv(t)
	if out := mustRun(t, "group", "ls"); !strings.Contains(out, "No groups yet") {
		t.Errorf("empty ls: %q", out)
	}

	setupProjects(t, "db", "api", "web")
	mustRun(t, "group", "create", "frontend", "web")
	mustRun(t, "group", "create", "backend", "db", "api")

	lines := strings.Split(strings.TrimSpace(mustRun(t, "group", "ls")), "\n")
	want := []string{
		"GROUP     PROJECTS",
		"backend   db, api",
		"frontend  web",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("group ls:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}
