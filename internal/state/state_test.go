package state

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
)

func TestDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, dir)
	if got, err := Dir(); err != nil || got != dir {
		t.Fatalf("Dir() = %q, %v", got, err)
	}
	t.Setenv(EnvVar, "")
	if got, err := Dir(); err != nil || filepath.Base(got) != "runr" {
		t.Fatalf("Dir() = %q, %v", got, err)
	}
}

func TestRecorder(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 6, 21, 0, 0, 0, time.UTC)
	r := NewRecorder(dir)
	r.now = func() time.Time { return at }

	r.SetArgs("api", " --verbose ") // nothing started yet: not saved
	if _, ok := LoadLast(dir); ok {
		t.Fatal("saved before anything was started")
	}
	r.Started("backend")
	r.Started("web", "backend") // in start order, once each
	r.SetArgs("web", "--port 3001")
	r.SetArgs("web", "  ") // blank: removed

	got, ok := LoadLast(dir)
	if !ok {
		t.Fatal("nothing saved")
	}
	if !slices.Equal(got.Names, []string{"backend", "web"}) || !maps.Equal(got.Args, map[string]string{"api": "--verbose"}) || !got.At.Equal(at) {
		t.Errorf("last = %+v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != lastFile {
		t.Errorf("files left: %v", entries)
	}

	// A new session replaces the old one.
	NewRecorder(dir).Started("db")
	if got, _ := LoadLast(dir); !slices.Equal(got.Names, []string{"db"}) || got.Args != nil {
		t.Errorf("after a new session: %+v", got)
	}
}

func TestRecorderWithoutDir(t *testing.T) {
	r := NewRecorder("")
	r.Started("api") // does nothing, and doesn't fail
	if _, ok := LoadLast(""); ok {
		t.Error("LoadLast(\"\") found a session")
	}
}

func TestLoadLastBroken(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{"not json", `{"names": []}`} {
		if err := os.WriteFile(filepath.Join(dir, lastFile), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if l, ok := LoadLast(dir); ok {
			t.Errorf("%q: loaded %+v", content, l)
		}
	}
}

func TestRegistered(t *testing.T) {
	cfg := config.New()
	cfg.Projects["api"] = &config.Project{Cmd: "x"}
	cfg.Projects["web"] = &config.Project{Cmd: "x"}
	cfg.Groups["backend"] = []string{"api"}
	l := LastSession{
		Names: []string{"backend", "gone", "web", "old-group"},
		Args:  map[string]string{"api": "--x", "gone": "--y"},
	}
	names, args, skipped := l.Registered(cfg)
	if !slices.Equal(names, []string{"backend", "web"}) || !maps.Equal(args, map[string]string{"api": "--x"}) || !slices.Equal(skipped, []string{"gone", "old-group"}) {
		t.Errorf("names %q, args %q, skipped %q", names, args, skipped)
	}
}
