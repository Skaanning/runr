package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

const oid = "43727e958c9af270d480914e3142764753de6ba0"

// Fixtures are real output of git 2.56: git status --porcelain=v2 --branch --untracked-files=no.
func TestParseStatus(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want Status
	}{{
		name: "unborn branch",
		out:  "# branch.oid (initial)\n# branch.head main\n",
		want: Status{Branch: "main"},
	}, {
		name: "clean with upstream",
		out:  "# branch.oid " + oid + "\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n",
		want: Status{Branch: "main", Commit: oid, Upstream: "origin/main"},
	}, {
		name: "ahead and behind",
		out:  "# branch.oid " + oid + "\n# branch.head feat/login\n# branch.upstream origin/feat/login\n# branch.ab +3 -12\n",
		want: Status{Branch: "feat/login", Commit: oid, Upstream: "origin/feat/login", Ahead: 3, Behind: 12},
	}, {
		name: "upstream gone",
		out:  "# branch.oid " + oid + "\n# branch.head feature\n# branch.upstream origin/feature\n",
		want: Status{Branch: "feature", Commit: oid, Upstream: "origin/feature", UpstreamGone: true},
	}, {
		name: "detached",
		out:  "# branch.oid " + oid + "\n# branch.head (detached)\n",
		want: Status{Commit: oid, Detached: true},
	}, {
		name: "modified",
		out: "# branch.oid " + oid + "\n# branch.head main\n" +
			"1 .M N... 100644 100644 100644 7898192261 7898192261 f\n",
		want: Status{Branch: "main", Commit: oid, Changed: true},
	}, {
		name: "renamed",
		out: "# branch.oid " + oid + "\n# branch.head main\n" +
			"2 RM N... 100644 100644 100644 7898192261 7898192261 R100 g\tf\n",
		want: Status{Branch: "main", Commit: oid, Changed: true},
	}, {
		name: "conflict",
		out: "# branch.oid " + oid + "\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +1 -1\n" +
			"u UU N... 100644 100644 100644 100644 7898192261 975fbec825 587be6b4c3 f\n",
		want: Status{Branch: "main", Commit: oid, Upstream: "origin/main", Ahead: 1, Behind: 1, Conflicts: true},
	}, {
		name: "untracked, ignored and unknown lines are skipped",
		out:  "# branch.oid " + oid + "\n# branch.head main\n# stash 2\n? new.txt\n! build/\nz future\n",
		want: Status{Branch: "main", Commit: oid},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStatus([]byte(tt.out))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestParseStatusBadAheadBehind(t *testing.T) {
	if _, err := parseStatus([]byte("# branch.ab nonsense\n")); err == nil {
		t.Error("expected an error")
	}
}

func TestShortCommit(t *testing.T) {
	if got := (Status{Commit: oid}).ShortCommit(); got != "43727e9" {
		t.Errorf("ShortCommit = %q", got)
	}
	if got := (Status{}).ShortCommit(); got != "" {
		t.Errorf("ShortCommit of unborn = %q", got)
	}
}

func TestOperationInProgress(t *testing.T) {
	tests := []struct {
		files []string // created in the git dir; a trailing / makes a directory
		want  Operation
	}{
		{nil, NoOperation},
		{[]string{"MERGE_HEAD"}, Merge},
		{[]string{"rebase-merge/"}, Rebase},
		{[]string{"rebase-apply/"}, Rebase},
		{[]string{"rebase-apply/", "rebase-apply/applying"}, Am},
		{[]string{"CHERRY_PICK_HEAD"}, CherryPick},
		{[]string{"REVERT_HEAD"}, Revert},
		{[]string{"ORIG_HEAD", "FETCH_HEAD"}, NoOperation},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		for _, f := range tt.files {
			path := filepath.Join(dir, filepath.FromSlash(f))
			var err error
			if f[len(f)-1] == '/' {
				err = os.Mkdir(path, 0o755)
			} else {
				err = os.WriteFile(path, nil, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := operationInProgress(dir); got != tt.want {
			t.Errorf("%v: got %v, want %v", tt.files, got, tt.want)
		}
	}
}
