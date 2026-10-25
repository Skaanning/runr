package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/proc"
)

// clipboard returns what cmd puts on the clipboard.
func clipboard(t *testing.T, cmd func() any) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("nothing copied")
	}
	return fmt.Sprint(cmd())
}

func TestCopyCommand(t *testing.T) {
	m, _ := newTestModel(t, 120, 20)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("PORT=3000\nSECRET=s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := m.cfg.Projects["db"]
	db.Dir, db.EnvFile, db.Env, db.Cmd = dir, ".env", map[string]string{"PORT": "8080"}, "go run ."

	// The defaults, then the env file, then env (which wins).
	want := proc.CommandLine(dir, []string{
		"CLICOLOR_FORCE=1", "FORCE_COLOR=1", "PYTHONUNBUFFERED=1", "PORT=8080", "SECRET=s3cret",
	}, "go run .")
	cmd := m.handleKey(key("c")) // db is selected
	if got := clipboard(t, func() any { return cmd() }); got != want {
		t.Fatalf("copied:\n%s\nwant:\n%s", got, want)
	}
	if m.notice != "copied: "+want {
		t.Fatalf("notice = %q", m.notice)
	}

	// A missing env file is reported instead.
	db.EnvFile = "gone.env"
	if cmd := m.handleKey(key("c")); cmd != nil || !strings.HasPrefix(m.notice, "can't copy the command of db: ") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestLauncherCopyCommand(t *testing.T) {
	cfg := launcherConfig(t)
	m := newTestLauncher(t, cfg)
	// The cursor starts on the backend group: nothing to copy.
	if _, cmd := m.Update(key("c")); cmd != nil {
		t.Fatal("copied a group")
	}
	m.Update(key("down")) // api
	if !strings.Contains(launcherHelp(m), "c  copy cmd") {
		t.Errorf("help = %q", launcherHelp(m))
	}
	_, cmd := m.Update(key("c"))
	p := cfg.Projects["api"]
	want := proc.CommandLine(p.Dir, proc.BuildEnv(nil, proc.DefaultEnv), p.Cmd)
	if got := clipboard(t, func() any { return cmd() }); got != want {
		t.Fatalf("copied:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasPrefix(launcherHelp(m), " copied: cd ") {
		t.Errorf("help = %q", launcherHelp(m))
	}
}
