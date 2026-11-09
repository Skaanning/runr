package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/config"
)

func TestAddStoresDirsUnderRunrRoot(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	t.Setenv(config.RootEnvVar, root)
	for _, d := range []string{"api", filepath.Join("apps", "web")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()

	t.Chdir(filepath.Join(root, "api"))
	out := mustRun(t, "add", "api", "--", "run")
	mustRun(t, "add", "web", "--dir", filepath.Join(root, "apps", "web"), "--", "run")
	mustRun(t, "add", "other", "--dir", outside, "--", "run")
	mustRun(t, "add", "kept", "--dir", "$RUNR_ROOT/apps", "--", "run")

	if !strings.Contains(out, "dir: $RUNR_ROOT/api") {
		t.Errorf("add output: %q", out)
	}
	p := loadConfig(t).Projects
	for name, want := range map[string]string{
		"api":   "$RUNR_ROOT/api",
		"web":   "$RUNR_ROOT/apps/web",
		"other": outside,
		"kept":  "$RUNR_ROOT/apps",
	} {
		if p[name].Dir != want {
			t.Errorf("%s.Dir = %q, want %q", name, p[name].Dir, want)
		}
	}

	// runr edit --dir switches an existing project to the root form.
	t.Setenv(config.RootEnvVar, "")
	mustRun(t, "add", "late", "--dir", filepath.Join(root, "api"), "--", "run")
	t.Setenv(config.RootEnvVar, root)
	mustRun(t, "edit", "late", "--dir", filepath.Join(root, "api"))
	if got := loadConfig(t).Projects["late"].Dir; got != "$RUNR_ROOT/api" {
		t.Errorf("late.Dir after edit = %q", got)
	}
}

func TestImportStoresDirUnderRunrRoot(t *testing.T) {
	testEnv(t)
	path := fixture(t, "Procfile")
	t.Setenv(config.RootEnvVar, filepath.Dir(filepath.Dir(path))) // testdata
	mustRun(t, "import", path)
	for name, p := range loadConfig(t).Projects {
		if p.Dir != "$RUNR_ROOT/import" {
			t.Errorf("%s.Dir = %q", name, p.Dir)
		}
	}
}

func TestUpNeedsRunrRootOnlyForItsProjects(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	t.Setenv(config.RootEnvVar, root)
	mustRun(t, "add", "shared", "--dir", root, "--", "true")
	mustRun(t, "add", "local", "--dir", t.TempDir(), "--", "true")

	t.Setenv(config.RootEnvVar, "")
	_, stderr, err := run(t, "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, `warning: project "shared": dir: $RUNR_ROOT is not set`) {
		t.Errorf("ls stderr: %q", stderr)
	}
	// Other projects can still be changed while the root is unset.
	mustRun(t, "edit", "local", "--color", "red")

	_, _, err = run(t, "up", "shared")
	wantErr(t, err, `cannot start:`)
	wantErr(t, err, `$RUNR_ROOT is not set`)
}
