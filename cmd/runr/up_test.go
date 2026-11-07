package main

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/state"
	"github.com/Skaanning/runr/internal/supervisor"
)

func TestUpWithoutTerminalPrintsPlain(t *testing.T) {
	testEnv(t)
	mustRun(t, "add", "hi", "--dir", t.TempDir(), "--", "echo hello")
	out, _, err := run(t, "up", "hi") // stdout is a buffer, not a terminal
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi   │ hello\n") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestUpLast(t *testing.T) {
	testEnv(t)
	mustRun(t, "add", "hi", "--dir", t.TempDir(), "--", "echo hello")
	_, _, err := run(t, "up", "--last")
	wantErr(t, err, "no last session yet")

	mustRun(t, "up", "hi") // recorded
	dir, err := state.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := state.LoadLast(dir); !ok || !slices.Equal(l.Names, []string{"hi"}) {
		t.Fatalf("last session after up hi: %+v", l)
	}

	// As if hi had run with arguments next to a project since removed.
	r := state.NewRecorder(dir)
	r.Started("hi", "gone")
	r.SetArgs("hi", "world")
	out, stderr, err := run(t, "up", "--last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello world\n") {
		t.Errorf("output without the arguments:\n%s", out)
	}
	if !strings.Contains(stderr, "runr: skipped from the last session, not registered any more: gone\n") {
		t.Errorf("stderr = %q", stderr)
	}
	// That run is the last session now, with the arguments.
	if l, _ := state.LoadLast(dir); !slices.Equal(l.Names, []string{"hi"}) || l.Args["hi"] != "world" {
		t.Errorf("last session after up --last: %+v", l)
	}

	_, _, err = run(t, "up", "--last", "hi")
	wantErr(t, err, "--last starts the last session; leave out the names")

	mustRun(t, "rm", "hi")
	_, _, err = run(t, "up", "--last")
	wantErr(t, err, "nothing from the last session is registered any more")
}

func TestUpReportsErrorsBeforeStarting(t *testing.T) {
	testEnv(t)
	mustRun(t, "add", "gone", "--dir", t.TempDir()+"/missing", "--", "true")
	_, _, err := run(t, "up", "gone")
	wantErr(t, err, "cannot start:")
	_, _, err = run(t, "up", "nope")
	wantErr(t, err, `unknown`)
}

func TestIsTerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	for name, f := range map[string]any{"buffer": &bytes.Buffer{}, "null device": devNull, "file": mustTemp(t)} {
		if isTerminal(f) {
			t.Errorf("%s reported as a terminal", name)
		}
	}
}

func mustTemp(t *testing.T) *os.File {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestSummarize(t *testing.T) {
	infos := []supervisor.ProcessInfo{
		{Name: "db", State: supervisor.Stopped},
		{Name: "worker", State: supervisor.Crashed, Exit: proc.ExitStatus{Code: 1}},
		{Name: "api", State: supervisor.Blocked},
		{Name: "web", State: supervisor.Crashed, Exit: proc.ExitStatus{Code: -1, Err: errors.New("exec: not found")}},
		{Name: "job", State: supervisor.Exited},
	}
	want := "worker crashed (exit code 1), api was blocked, web crashed (exec: not found)"
	if got := summarize(infos); got != want {
		t.Fatalf("summarize = %q, want %q", got, want)
	}
	if got := summarize(infos[:1]); got != "" {
		t.Fatalf("summarize = %q, want empty", got)
	}
}

func TestLauncherNeedsTerminal(t *testing.T) {
	testEnv(t)
	_, _, err := run(t)
	wantErr(t, err, "the launcher needs a terminal")
}
