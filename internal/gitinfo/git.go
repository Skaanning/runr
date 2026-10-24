// Package gitinfo reads the git state of project directories: which
// repository a directory belongs to, its branch, upstream, changes and any
// operation in progress.
package gitinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNotInstalled is returned by New when git is not on the PATH.
var ErrNotInstalled = errors.New("git is not installed")

// DefaultTimeout bounds every git command.
const DefaultTimeout = 60 * time.Second

// Git runs git commands non-interactively: with a timeout, no stdin, no
// credential prompts and SSH in batch mode.
type Git struct {
	path    string
	Timeout time.Duration // per command; DefaultTimeout if zero
}

// New finds the git executable. It returns ErrNotInstalled if there is none,
// in which case git features should be hidden.
func New() (*Git, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNotInstalled
	}
	return &Git{path: path}, nil
}

// Error is a git command that failed.
type Error struct {
	Args   []string // git arguments, without the global options runr adds
	Stderr string   // git's error output, trimmed
	Err    error    // exit status, or context.DeadlineExceeded on timeout
}

func (e *Error) Error() string {
	msg := firstLine(e.Stderr)
	switch {
	case errors.Is(e.Err, context.DeadlineExceeded):
		msg = "timed out"
	case msg == "":
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", e.Args[0], msg)
}

func (e *Error) Unwrap() error { return e.Err }

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// run runs git in dir and returns its standard output. extraEnv is added to
// the environment built by gitEnv.
func (g *Git) run(ctx context.Context, dir string, extraEnv []string, args ...string) ([]byte, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, g.path, args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(os.Environ()), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Don't wait forever for output from children (e.g. ssh) that outlive git.
	cmd.WaitDelay = time.Second
	detach(cmd)

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return stdout.Bytes(), &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

// repoEnv are variables that point git at a specific repository. They are
// removed so that each project's repository is found from its directory, even
// if runr was started from inside a git hook or similar.
var repoEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE",
	"GIT_PREFIX",
}

// gitEnv returns base with the settings that keep git from prompting: no
// terminal credential prompts, and SSH in batch mode unless the user
// configured their own SSH command through the environment.
func gitEnv(base []string) []string {
	env := make([]string, 0, len(base)+2)
	customSSH := false
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case isRepoEnv(key):
			continue
		case key == "GIT_SSH_COMMAND" || key == "GIT_SSH":
			customSSH = true
		case key == "GIT_TERMINAL_PROMPT":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if !customSSH {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}

func isRepoEnv(key string) bool {
	for _, k := range repoEnv {
		if strings.EqualFold(key, k) {
			return true
		}
	}
	return false
}
