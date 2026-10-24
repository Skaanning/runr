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
	Stderr string   // git's error output (for actions, all of its output)
	Err    error    // exit status, or context.DeadlineExceeded on timeout
}

func (e *Error) Error() string {
	msg := errorLine(e.Stderr)
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

// errorLine returns the line of git's output that says what went wrong: the
// first "fatal:" or "error:" line, skipping progress and hints, or else the
// first line.
func errorLine(s string) string {
	for line := range strings.Lines(s) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "error:") {
			return line
		}
	}
	return firstLine(s)
}

// run runs git in dir and returns its standard output. extraEnv is added to
// the environment built by gitEnv.
func (g *Git) run(ctx context.Context, dir string, extraEnv []string, args ...string) ([]byte, error) {
	return g.invoke(ctx, dir, extraEnv, false, args...)
}

// invoke runs git in dir. With combined, standard output and error are
// returned together, interleaved, and also make up Error.Stderr on failure.
func (g *Git) invoke(ctx context.Context, dir string, extraEnv []string, combined bool, args ...string) ([]byte, error) {
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
	if combined {
		cmd.Stderr = &stdout
	}
	// Don't wait forever for output from children (e.g. ssh) that outlive git.
	cmd.WaitDelay = time.Second
	detach(cmd)

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		msg := stderr.String()
		if combined {
			msg = stdout.String()
		}
		return stdout.Bytes(), &Error{Args: args, Stderr: msg, Err: err}
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
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if isRepoEnv(key) || key == "GIT_TERMINAL_PROMPT" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if !customSSH(base) {
		env = append(env, "GIT_SSH_COMMAND="+batchSSH("ssh"))
	}
	return env
}

// customSSH reports whether env chooses the SSH command git runs.
func customSSH(env []string) bool {
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "GIT_SSH_COMMAND" || key == "GIT_SSH" {
			return true
		}
	}
	return false
}

// batchSSH returns cmd with BatchMode on if it runs OpenSSH's ssh, so that
// ssh can't prompt. Other programs may not understand -o and get cmd
// unchanged.
func batchSSH(cmd string) string {
	if sshProgram(cmd) != "ssh" {
		return cmd
	}
	return cmd + " -o BatchMode=yes"
}

// sshProgram returns the base name of the program a command line runs,
// without ".exe".
func sshProgram(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	var prog string
	if q := cmd[:min(1, len(cmd))]; q == `"` || q == "'" {
		prog, _, _ = strings.Cut(cmd[1:], q)
	} else if f := strings.Fields(cmd); len(f) > 0 {
		prog = f[0]
	}
	prog = prog[strings.LastIndexAny(prog, `/\`)+1:]
	return strings.TrimSuffix(strings.ToLower(prog), ".exe")
}

func isRepoEnv(key string) bool {
	for _, k := range repoEnv {
		if strings.EqualFold(key, k) {
			return true
		}
	}
	return false
}
