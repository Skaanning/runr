//go:build !windows

package gitinfo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A stand-in for git: /bin/sh, so the tests control what the command does.
func shGit() *Git { return &Git{path: "/bin/sh"} }

func TestTimeoutKillsChildren(t *testing.T) {
	g := shGit()
	g.Timeout = 200 * time.Millisecond
	start := time.Now()
	// The background sleep keeps stdout open; it must be killed too.
	_, err := g.run(context.Background(), t.TempDir(), nil, "-c", "sleep 30 & sleep 30")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Errorf("took %v; children were not killed with git", elapsed)
	}
}

func TestRunsInOwnSession(t *testing.T) {
	// "sess" is the session ID on Linux and the session's address on macOS
	// (whose ps has no "sid"); either way it differs from the parent's (this
	// test) only in a new session.
	out, err := shGit().run(context.Background(), t.TempDir(), nil, "-c", "ps -o sess= -p $$; ps -o sess= -p $PPID")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] == fields[1] {
		t.Errorf("session of git and of runr = %q, want different (a new session)", fields)
	}
}
