package gitinfo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Operation is a multi-step git operation in progress in a work tree.
type Operation int

const (
	NoOperation Operation = iota
	Merge
	Rebase
	CherryPick
	Revert
	Am // git am (applying patches)
)

func (o Operation) String() string {
	return [...]string{"", "merge", "rebase", "cherry-pick", "revert", "am"}[o]
}

// Status is the state of a work tree.
type Status struct {
	Branch   string // current branch; "" when detached
	Commit   string // HEAD commit id; "" before the first commit
	Detached bool

	Upstream     string // upstream branch, e.g. "origin/main"; "" if none
	UpstreamGone bool   // an upstream is configured but no longer exists
	Ahead        int    // commits ahead of the upstream, as of the last fetch
	Behind       int    // commits behind the upstream, as of the last fetch

	Changed   bool      // tracked files with staged or unstaged changes
	Conflicts bool      // unmerged entries
	Operation Operation // merge, rebase, … in progress
}

// ShortCommit returns the abbreviated HEAD commit id.
func (s Status) ShortCommit() string {
	return s.Commit[:min(7, len(s.Commit))]
}

// Status reads the state of repo. Untracked files are not looked at, and the
// index is never locked or rewritten, so it is safe to call while the user is
// running git in the same repository.
func (g *Git) Status(ctx context.Context, repo Repo) (Status, error) {
	out, err := g.run(ctx, repo.Root, nil,
		"--no-optional-locks", "status", "--porcelain=v2", "--branch", "--untracked-files=no")
	if err != nil {
		return Status{}, err
	}
	s, err := parseStatus(out)
	if err != nil {
		return Status{}, err
	}
	s.Operation = operationInProgress(repo.GitDir)
	return s, nil
}

// parseStatus parses the output of git status --porcelain=v2 --branch.
func parseStatus(out []byte) (Status, error) {
	var s Status
	aheadBehind := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		kind, rest, _ := strings.Cut(line, " ")
		switch kind {
		case "#":
			key, value, _ := strings.Cut(rest, " ")
			switch key {
			case "branch.oid":
				if value != "(initial)" {
					s.Commit = value
				}
			case "branch.head":
				if value == "(detached)" {
					s.Detached = true
				} else {
					s.Branch = value
				}
			case "branch.upstream":
				s.Upstream = value
			case "branch.ab":
				if _, err := fmt.Sscanf(value, "+%d -%d", &s.Ahead, &s.Behind); err != nil {
					return Status{}, fmt.Errorf("git status: bad line %q", line)
				}
				aheadBehind = true
			}
		case "1", "2": // ordinary and renamed/copied entries
			s.Changed = true
		case "u":
			s.Conflicts = true
		}
		// "?" and "!" (untracked, ignored) and unknown lines are skipped.
	}
	if err := sc.Err(); err != nil {
		return Status{}, err
	}
	s.UpstreamGone = s.Upstream != "" && !aheadBehind
	return s, nil
}

// operationInProgress checks the git dir for the files git keeps while an
// operation waits for the user.
func operationInProgress(gitDir string) Operation {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("rebase-merge"):
		return Rebase
	case exists(filepath.Join("rebase-apply", "applying")):
		return Am
	case exists("rebase-apply"):
		return Rebase
	case exists("MERGE_HEAD"):
		return Merge
	case exists("CHERRY_PICK_HEAD"):
		return CherryPick
	case exists("REVERT_HEAD"):
		return Revert
	}
	return NoOperation
}
