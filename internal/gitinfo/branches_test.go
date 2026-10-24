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
		"refs/heads/feat/login\x00 \x00origin/feat/login\x002 hours ago\x00",
		"refs/heads/main\x00*\x00origin/main\x003 days ago\x00",
		"refs/remotes/origin/HEAD\x00 \x00\x003 days ago\x00refs/remotes/origin/main",
		"refs/remotes/origin/main\x00 \x00\x003 days ago\x00",
		"refs/remotes/origin/fix/typo\x00 \x00\x005 days ago\x00",
		"refs/remotes/upstream/feat/login\x00 \x00\x001 week ago\x00",
		"",
	}, "\n")
	got, err := parseBranches(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Branch{
		{Name: "feat/login", Upstream: "origin/feat/login", Date: "2 hours ago"},
		{Name: "main", Current: true, Upstream: "origin/main", Date: "3 days ago"},
		{Name: "origin/fix/typo", Remote: true, Date: "5 days ago"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got[2].LocalName() != "fix/typo" || got[0].LocalName() != "feat/login" {
		t.Errorf("LocalName = %q, %q", got[2].LocalName(), got[0].LocalName())
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
		if b.Name == "main" && (!b.Current || b.Upstream != "origin/main" || b.Date == "") {
			t.Errorf("main = %+v", b)
		}
	}
	// Same commit date for all, so only local-before-remote is fixed.
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
