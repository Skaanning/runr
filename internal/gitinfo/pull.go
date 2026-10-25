package gitinfo

import (
	"context"
	"os"
	"strings"
)

// Pull fast-forwards the current branch of repo to its upstream; it fails
// rather than merge or rebase. It returns git's output, standard output and
// error interleaved, in the user's language.
func (g *Git) Pull(ctx context.Context, repo Repo) (string, error) {
	out, err := g.invoke(ctx, repo.Root, g.remoteEnv(ctx, repo), true, PullArgs()...)
	return string(out), err
}

// PullArgs returns the git arguments Pull runs. --no-rebase keeps a
// pull.rebase setting from turning it into a rebase on any git version.
func PullArgs() []string {
	return []string{"pull", "--ff-only", "--no-rebase"}
}

// Fetch updates repo's remote-tracking branches from its default remote,
// leaving the working tree alone. It returns git's output like Pull.
func (g *Git) Fetch(ctx context.Context, repo Repo) (string, error) {
	out, err := g.invoke(ctx, repo.Root, g.remoteEnv(ctx, repo), true, FetchArgs()...)
	return string(out), err
}

// FetchArgs returns the git arguments Fetch runs.
func FetchArgs() []string {
	return []string{"fetch"}
}

// remoteEnv returns the environment for an operation that contacts a
// remote (pull, fetch). The GIT_SSH_COMMAND that keeps ssh from prompting
// would take precedence over a core.sshCommand the user configured, so that
// one is passed on instead, in batch mode if it runs ssh.
func (g *Git) remoteEnv(ctx context.Context, repo Repo) []string {
	if customSSH(os.Environ()) {
		return nil
	}
	out, err := g.run(ctx, repo.Root, nil, "config", "--get", "core.sshCommand")
	if cmd := strings.TrimSpace(string(out)); err == nil && cmd != "" {
		return []string{"GIT_SSH_COMMAND=" + batchSSH(cmd)} // later entries win
	}
	return nil
}
