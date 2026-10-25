package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/config"
)

func TestLogDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	cfg := config.New()

	if dir, err := logDir(cfg, ""); err != nil || dir != "" {
		t.Errorf("none set: %q, %v", dir, err)
	}
	cfg.Settings.LogDirValue = "~/logs/runr"
	dir, err := logDir(cfg, "")
	if want := filepath.Join(home, "logs", "runr"); err != nil || dir != want {
		t.Errorf("setting: %q, %v; want %q", dir, err, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("not created: %v", err)
	}
	// The flag wins.
	dir, err = logDir(cfg, "~/other")
	if want := filepath.Join(home, "other"); err != nil || dir != want {
		t.Errorf("flag: %q, %v; want %q", dir, err, want)
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := logDir(cfg, file); err == nil || !strings.HasPrefix(err.Error(), "log dir: ") {
		t.Errorf("file: %v", err)
	}
}
