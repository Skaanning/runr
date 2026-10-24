package gitinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseBranches(t *testing.T) {
	out := strings.Join([]string{
		"refs/heads/feat/login\x00 \x00origin/feat/login\x002 hours ago\x00\x00ahead 2, behind 1\x00Add session cookie",
		"refs/heads/main\x00*\x00origin/main\x003 days ago\x00\x00ahead 1\x00Merge pull request #42",
		"refs/heads/old\x00 \x00origin/old\x001 year ago\x00\x00gone\x00Old work",
		"refs/heads/wip\x00 \x00\x001 minute ago\x00\x00\x00",
		"refs/remotes/origin/HEAD\x00 \x00\x003 days ago\x00refs/remotes/origin/main\x00\x00Merge pull request #42",
		"refs/remotes/origin/main\x00 \x00\x003 days ago\x00\x00\x00Merge pull request #42",
		"refs/remotes/origin/fix/typo\x00 \x00\x005 days ago\x00\x00\x00Fix typo in README",
		"refs/remotes/upstream/feat/login\x00 \x00\x001 week ago\x00\x00\x00Login",
		"",
	}, "\n")
	got, err := parseBranches(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Branch{
		{Name: "feat/login", Upstream: "origin/feat/login", Ahead: 2, Behind: 1, Date: "2 hours ago", Subject: "Add session cookie"},
		{Name: "main", Current: true, Upstream: "origin/main", Ahead: 1, Date: "3 days ago", Subject: "Merge pull request #42"},
		{Name: "old", Upstream: "origin/old", Date: "1 year ago", Subject: "Old work"},
		{Name: "wip", Date: "1 minute ago"},
		{Name: "origin/fix/typo", Remote: true, Date: "5 days ago", Subject: "Fix typo in README"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got[4].LocalName() != "fix/typo" || got[0].LocalName() != "feat/login" {
		t.Errorf("LocalName = %q, %q", got[4].LocalName(), got[0].LocalName())
	}
}

func TestCheckSafe(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{Status{Branch: "main", Ahead: 2, Behind: 1}, ""},
		{Status{Changed: true}, "there are uncommitted changes to tracked files"},
		{Status{Conflicts: true, Changed: true}, "there are unresolved conflicts"},
		{Status{Operation: Rebase, Conflicts: true}, "a rebase is in progress"},
		{Status{Operation: CherryPick}, "a cherry-pick is in progress"},
	}
	for _, tt := range tests {
		err := tt.s.CheckSafe()
		if got := ""; err != nil {
			got = err.Error()
			if got != tt.want {
				t.Errorf("%+v: %q, want %q", tt.s, got, tt.want)
			}
		} else if tt.want != "" {
			t.Errorf("%+v: nil, want %q", tt.s, tt.want)
		}
	}
}

func TestBranchesAndSwitch(t *testing.T) {
	g := newGit(t)
	upstream, clone := newClone(t)
	git(t, upstream, "branch", "feature")
	git(t, clone, "fetch", "-q")
	git(t, clone, "branch", "local-only")
	writeFile(t, filepath.Join(clone, "ahead"), "ahead\n")
	git(t, clone, "add", "ahead")
	git(t, clone, "commit", "-q", "-m", "Local commit")
	repo, err := g.Find(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}

	branches, err := g.Branches(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range branches {
		names = append(names, b.Name)
		if b.Name == "main" && (!b.Current || b.Upstream != "origin/main" || b.Date == "" || b.Ahead != 1 || b.Behind != 0 || b.Subject != "Local commit") {
			t.Errorf("main = %+v", b)
		}
		if b.Name == "origin/feature" && b.Subject == "" {
			t.Errorf("origin/feature = %+v", b)
		}
	}
	// Commit dates may be equal, so only local-before-remote is fixed.
	if len(names) != 3 || names[2] != "origin/feature" || !branches[2].Remote {
		t.Fatalf("branches = %v", names)
	}

	out, err := g.Switch(context.Background(), repo, Branch{Name: "origin/feature", Remote: true})
	if err != nil {
		t.Fatalf("switch: %v\n%s", err, out)
	}
	if !strings.Contains(out, "feature") {
		t.Errorf("output = %q", out)
	}
	if s := status(t, g, clone); s.Branch != "feature" || s.Upstream != "origin/feature" {
		t.Errorf("after switching to a remote branch: %+v", s)
	}

	if _, err := g.Switch(context.Background(), repo, Branch{Name: "local-only"}); err != nil {
		t.Fatal(err)
	}
	if s := status(t, g, clone); s.Branch != "local-only" {
		t.Errorf("after switching: %+v", s)
	}
}

func TestSwitchFailureKeepsOutput(t *testing.T) {
	g := newGit(t)
	root := newRepo(t)
	git(t, root, "checkout", "-q", "-b", "other")
	writeFile(t, filepath.Join(root, "new"), "tracked on other\n")
	git(t, root, "add", "new")
	git(t, root, "commit", "-q", "-m", "add new")
	git(t, root, "checkout", "-q", "main")
	// An untracked file in the way: git refuses, runr's preflight doesn't.
	writeFile(t, filepath.Join(root, "new"), "untracked\n")
	if err := status(t, g, root).CheckSafe(); err != nil {
		t.Fatalf("untracked files should not block: %v", err)
	}

	t.Setenv("LC_ALL", "C")
	repo := Repo{Root: root, GitDir: filepath.Join(root, ".git")}
	out, err := g.Switch(context.Background(), repo, Branch{Name: "other"})
	var gitErr *Error
	if !errors.As(err, &gitErr) || !strings.Contains(out, "untracked working tree files would be overwritten") ||
		gitErr.Stderr != out || !strings.HasPrefix(err.Error(), "git switch: error: ") {
		t.Errorf("err = %v\noutput = %q", err, out)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "new")); string(data) != "untracked\n" {
		t.Error("the untracked file was changed")
	}
}
