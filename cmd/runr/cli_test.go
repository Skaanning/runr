package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/config"
)

// testEnv points runr at a fresh config file, and session files (Linux) at
// a fresh directory, for the duration of the test.
func testEnv(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv(config.EnvVar, path)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return path
}

// run executes runr with args and returns stdout, stderr and the error.
func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// mustRun executes runr and fails the test on error.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, _, err := run(t, args...)
	if err != nil {
		t.Fatalf("runr %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func loadConfig(t *testing.T) *config.Config {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func wantErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("err = %v, want containing %q", err, substr)
	}
}
