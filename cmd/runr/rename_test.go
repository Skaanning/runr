package main

import (
	"slices"
	"strings"
	"testing"
)

func TestRename(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	mustRun(t, "add", "db", "--dir", dir, "--", "x")
	mustRun(t, "add", "api", "--dir", dir, "--depends-on", "db", "--", "x")
	mustRun(t, "group", "create", "backend", "db", "api")

	out := mustRun(t, "rename", "db", "postgres")
	for _, want := range []string{`Renamed project "db" to "postgres"`, "updated groups: backend", "updated depends_on of: api"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	c := loadConfig(t)
	if c.Projects["db"] != nil || c.Projects["postgres"] == nil ||
		!slices.Equal(c.Projects["api"].DependsOn, []string{"postgres"}) ||
		!slices.Equal(c.Groups["backend"], []string{"postgres", "api"}) {
		t.Fatalf("config after rename: %+v %v", c.Projects, c.Groups)
	}

	if out := mustRun(t, "rename", "backend", "server"); out != "Renamed group \"backend\" to \"server\"\n" {
		t.Fatalf("output = %q", out)
	}
	if c := loadConfig(t); c.Groups["server"] == nil || c.Groups["backend"] != nil {
		t.Fatalf("groups = %v", c.Groups)
	}

	_, _, err := run(t, "rename", "api", "server")
	wantErr(t, err, `a project or group named "server" already exists`)
	_, _, err = run(t, "rename", "nope", "x")
	wantErr(t, err, `unknown project or group "nope"`)
	_, _, err = run(t, "rename", "api", "bad name")
	wantErr(t, err, `invalid name "bad name"`)
}
