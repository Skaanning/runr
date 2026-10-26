//go:build !windows

package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
	// Far below the 30 s the children would keep it waiting, so a slow CI
	// runner doesn't fail it.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v; children were not killed with git", elapsed)
	}
}

func TestRunsInOwnSession(t *testing.T) {
	t.Setenv(helperEnv, "session")
	out, err := (&Git{path: os.Args[0]}).run(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != fields[1] {
		t.Errorf("pid and session id = %q, want equal (a new session)", fields)
	}
}

// printSession prints the process's pid and session ID (macOS's ps has no
// column for the latter).
func printSession() {
	sid, err := unix.Getsid(0)
	if err != nil {
		fmt.Println("error", err)
		return
	}
	fmt.Println(os.Getpid(), sid)
}
