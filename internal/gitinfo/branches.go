package gitinfo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Branch is a local or remote-tracking branch.
type Branch struct {
	Name     string // "main", or "origin/main" for a remote-tracking branch
	Remote   bool   // a remote-tracking branch
	Current  bool   // checked out in this work tree
	Upstream string // for a local branch: its upstream, e.g. "origin/main"
	Ahead    int    // commits not in the upstream
	Behind   int    // commits in the upstream, not in the branch
	Date     string // last commit, relative ("3 days ago")
	Subject  string // last commit's subject line
}

// LocalName is the name of the local branch for b: for a remote-tracking
// branch, its name without the remote ("origin/feat/x" → "feat/x").
func (b Branch) LocalName() string {
	if !b.Remote {
		return b.Name
	}
	_, name, _ := strings.Cut(b.Name, "/")
	return name
}

// Branches lists the local branches and then the remote-tracking branches
// that have no local branch of the same name, each most recently committed
// first.
func (g *Git) Branches(ctx context.Context, repo Repo) ([]Branch, error) {
	out, err := g.run(ctx, repo.Root, []string{"LC_ALL=C"}, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%00%(HEAD)%00%(upstream:short)%00%(committerdate:relative)%00%(symref)"+
			"%00%(upstream:track,nobracket)%00%(contents:subject)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	return parseBranches(string(out))
}

func parseBranches(out string) ([]Branch, error) {
	var local, remote []Branch
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\n")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) != 7 {
			return nil, fmt.Errorf("git for-each-ref: unexpected line %q", line)
		}
		ref, head, upstream, date, symref, track, subject := f[0], f[1], f[2], f[3], f[4], f[5], f[6]
		if symref != "" {
			continue // e.g. origin/HEAD
		}
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			b := Branch{Name: name, Current: head == "*", Upstream: upstream, Date: date, Subject: subject}
			b.Ahead, b.Behind = parseTrack(track)
			local = append(local, b)
		} else if name, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
			remote = append(remote, Branch{Name: name, Remote: true, Date: date, Subject: subject})
		}
	}
	hasLocal := map[string]bool{}
	for _, b := range local {
		hasLocal[b.Name] = true
	}
	branches := local
	for _, b := range remote {
		if !hasLocal[b.LocalName()] {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// parseTrack parses %(upstream:track,nobracket): "ahead 2, behind 1",
// "ahead 2", "behind 1", "gone" or empty.
func parseTrack(track string) (ahead, behind int) {
	for part := range strings.SplitSeq(track, ", ") {
		if n, ok := strings.CutPrefix(part, "ahead "); ok {
			ahead, _ = strconv.Atoi(n)
		} else if n, ok := strings.CutPrefix(part, "behind "); ok {
			behind, _ = strconv.Atoi(n)
		}
	}
	return ahead, behind
}

// Switch checks out branch b in repo. For a remote-tracking branch it
// creates a local branch tracking it. It returns git's output, standard
// output and error interleaved, in the user's language.
func (g *Git) Switch(ctx context.Context, repo Repo, b Branch) (string, error) {
	out, err := g.invoke(ctx, repo.Root, nil, true, SwitchArgs(b)...)
	return string(out), err
}

// SwitchArgs returns the git arguments Switch runs for b.
func SwitchArgs(b Branch) []string {
	if b.Remote {
		return []string{"switch", "--track", b.Name}
	}
	return []string{"switch", b.Name}
}
