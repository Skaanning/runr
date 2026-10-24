package gitinfo

import (
	"context"
	"os"
	"strings"
)

// Pull fast-forwards the current branch of repo to its upstream; it fails
// rather than merge or rebase. It returns git's output, standard output and
// error interleaved, in the user's language.
//
// Pull is the only operation that contacts a remote. The GIT_SSH_COMMAND
// that keeps ssh from prompting would take precedence over a core.sshCommand
// the user configured, so that one is passed on instead, in batch mode if it
// runs ssh.
func (g *Git) Pull(ctx context.Context, repo Repo) (string, error) {
	var env []string
	if !customSSH(os.Environ()) {
		out, err := g.run(ctx, repo.Root, nil, "config", "--get", "core.sshCommand")
		if cmd := strings.TrimSpace(string(out)); err == nil && cmd != "" {
			env = []string{"GIT_SSH_COMMAND=" + batchSSH(cmd)} // later entries win
		}
	}
	out, err := g.invoke(ctx, repo.Root, env, true, PullArgs()...)
	return string(out), err
}

// PullArgs returns the git arguments Pull runs. --no-rebase keeps a
// pull.rebase setting from turning it into a rebase on any git version.
func PullArgs() []string {
	return []string{"pull", "--ff-only", "--no-rebase"}
}
