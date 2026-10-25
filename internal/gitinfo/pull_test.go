package gitinfo

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPull(t *testing.T) {
	g := newGit(t)
	upstream, clone := newClone(t)
	repo, err := g.Find(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(upstream, "f"), "two\n")
	git(t, upstream, "commit", "-q", "-am", "two")
	want := strings.TrimSpace(git(t, upstream, "rev-parse", "HEAD"))
	out, err := g.Pull(context.Background(), repo)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if s := status(t, g, clone); s.Commit != want || s.Behind != 0 {
		t.Errorf("after pull: %+v, want commit %s", s, want)
	}
	if _, err := g.Pull(context.Background(), repo); err != nil {
		t.Errorf("up to date: %v", err)
	}

	// Diverged: fails, even when pull.rebase asks for a rebase.
	git(t, clone, "config", "pull.rebase", "true")
	writeFile(t, filepath.Join(upstream, "f"), "three\n")
	git(t, upstream, "commit", "-q", "-am", "three")
	writeFile(t, filepath.Join(clone, "g"), "local\n")
	git(t, clone, "add", "g")
	git(t, clone, "commit", "-q", "-m", "local")
	local := strings.TrimSpace(git(t, clone, "rev-parse", "HEAD"))
	t.Setenv("LC_ALL", "C")
	out, err = g.Pull(context.Background(), repo)
	if err == nil {
		t.Fatalf("diverged pull succeeded:\n%s", out)
	}
	if !strings.HasPrefix(err.Error(), "git pull: fatal: ") {
		t.Errorf("err = %v", err)
	}
	if s := status(t, g, clone); s.Commit != local || s.Operation != NoOperation || s.Ahead != 1 || s.Behind != 1 {
		t.Errorf("after the failed pull: %+v", s)
	}
}

// TestPullSSHCommand checks that a configured core.sshCommand is used, with
// BatchMode added only when it runs ssh.
func TestPullSSHCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts")
	}
	g := newGit(t)
	_, clone := newClone(t)
	git(t, clone, "remote", "set-url", "origin", "ssh://git.example.invalid/repo.git")
	repo, err := g.Find(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}
	unsetenv(t, "GIT_SSH_COMMAND")
	unsetenv(t, "GIT_SSH")

	bin := realDir(t)
	for _, tt := range []struct{ prog, batch string }{
		{"ssh", " -o BatchMode=yes"},
		{"my-ssh-wrapper", ""},
	} {
		prog := filepath.Join(bin, tt.prog)
		log := prog + ".log"
		writeFile(t, prog, "#!/bin/sh\necho \"$@\" >> '"+log+"'\nexit 1\n")
		if err := os.Chmod(prog, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, clone, "config", "core.sshCommand", prog+" -i key")
		if _, err := g.Pull(context.Background(), repo); err == nil {
			t.Fatalf("%s: pull succeeded", tt.prog)
		}
		args, err := os.ReadFile(log)
		if err != nil {
			t.Fatalf("%s was not run: %v", tt.prog, err)
		}
		if !strings.HasPrefix(string(args), "-i key"+tt.batch+" ") {
			t.Errorf("%s ran with %q", tt.prog, args)
		}
	}

	// A command chosen through the environment is left alone.
	t.Setenv("GIT_SSH_COMMAND", filepath.Join(bin, "my-ssh-wrapper")+" -i other")
	os.Remove(filepath.Join(bin, "my-ssh-wrapper.log"))
	g.Pull(context.Background(), repo)
	if args, _ := os.ReadFile(filepath.Join(bin, "my-ssh-wrapper.log")); !strings.HasPrefix(string(args), "-i other ") {
		t.Errorf("GIT_SSH_COMMAND: ran with %q", args)
	}
}

// unsetenv removes an environment variable for the rest of the test.
func unsetenv(t *testing.T, key string) {
	t.Setenv(key, "") // restores the original value afterwards
	os.Unsetenv(key)
}

func TestCheckPull(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{Status{Branch: "main", Upstream: "origin/main", Behind: 2}, ""},
		{Status{Branch: "main", Upstream: "origin/main", Changed: true}, "there are uncommitted changes to tracked files"},
		{Status{Commit: "abc", Detached: true}, "HEAD is detached, not on a branch"},
		{Status{Branch: "local"}, "local has no upstream branch"},
		{Status{Branch: "x", Upstream: "origin/x", UpstreamGone: true}, "the upstream branch origin/x no longer exists"},
	}
	for _, tt := range tests {
		got := ""
		if err := tt.s.CheckPull(); err != nil {
			got = err.Error()
		}
		if got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestBatchSSH(t *testing.T) {
	tests := map[string]string{
		"ssh":                           "ssh -o BatchMode=yes",
		"/usr/bin/ssh -i ~/.ssh/k":      "/usr/bin/ssh -i ~/.ssh/k -o BatchMode=yes",
		`"C:\Program Files\ssh.EXE" -v`: `"C:\Program Files\ssh.EXE" -v -o BatchMode=yes`,
		"'/opt/my ssh/ssh'":             "'/opt/my ssh/ssh' -o BatchMode=yes",
		"plink -batch":                  "plink -batch",
		"sshpass -f pw ssh":             "sshpass -f pw ssh",
		"":                              "",
	}
	for cmd, want := range tests {
		if got := batchSSH(cmd); got != want {
			t.Errorf("batchSSH(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestErrorLine(t *testing.T) {
	out := "From /r/up\n   d8ae078..fac60b4  main -> origin/main\nhint: Diverging branches can't be fast-forwarded\nfatal: Not possible to fast-forward, aborting.\n"
	if got := errorLine(out); got != "fatal: Not possible to fast-forward, aborting." {
		t.Errorf("got %q", got)
	}
	if got := errorLine("\nsomething odd\nmore\n"); got != "something odd" {
		t.Errorf("no error line: got %q", got)
	}
}

func TestFetch(t *testing.T) {
	g := newGit(t)
	upstream, clone := newClone(t)
	repo, err := g.Find(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}
	before := status(t, g, clone).Commit

	writeFile(t, filepath.Join(upstream, "f"), "two\n")
	git(t, upstream, "commit", "-q", "-am", "two")
	if out, err := g.Fetch(context.Background(), repo); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The remote-tracking branch moved; the working tree didn't.
	if s := status(t, g, clone); s.Commit != before || s.Behind != 1 {
		t.Errorf("after fetch: %+v", s)
	}

	t.Setenv("LC_ALL", "C")
	git(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone"))
	if _, err := g.Fetch(context.Background(), repo); err == nil || !strings.HasPrefix(err.Error(), "git fetch: ") {
		t.Errorf("missing remote: err = %v", err)
	}
}

func TestFetchSSHCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts")
	}
	g := newGit(t)
	_, clone := newClone(t)
	git(t, clone, "remote", "set-url", "origin", "ssh://git.example.invalid/repo.git")
	repo, err := g.Find(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}
	unsetenv(t, "GIT_SSH_COMMAND")
	unsetenv(t, "GIT_SSH")
	prog := filepath.Join(realDir(t), "ssh")
	writeFile(t, prog, "#!/bin/sh\necho \"$@\" >> '"+prog+".log'\nexit 1\n")
	if err := os.Chmod(prog, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "config", "core.sshCommand", prog+" -i key")
	if _, err := g.Fetch(context.Background(), repo); err == nil {
		t.Fatal("fetch succeeded")
	}
	if args, _ := os.ReadFile(prog + ".log"); !strings.HasPrefix(string(args), "-i key -o BatchMode=yes ") {
		t.Errorf("ssh ran with %q", args)
	}
}
