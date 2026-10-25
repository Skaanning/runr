package proc

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOutputStreams(t *testing.T) {
	p, c := startHelper(t, "echo", Spec{})
	status := waitDone(t, p, 5*time.Second)

	if !status.Success() || status.String() != "exit code 0" {
		t.Errorf("status = %+v", status)
	}
	if got := c.texts(Stdout); !reflect.DeepEqual(got, []string{"out1", "out2"}) {
		t.Errorf("stdout = %q", got)
	}
	if got := c.texts(Stderr); !reflect.DeepEqual(got, []string{"err1"}) {
		t.Errorf("stderr = %q", got)
	}
}

func TestExitCode(t *testing.T) {
	p, _ := startHelper(t, "exit3", Spec{})
	status := waitDone(t, p, 5*time.Second)
	if status.Success() || status.Code != 3 || status.String() != "exit code 3" {
		t.Errorf("status = %+v", status)
	}
}

func TestDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	p, c := startHelper(t, "pwd", Spec{Dir: dir})
	waitDone(t, p, 5*time.Second)

	got, _ := filepath.EvalSymlinks(strings.Join(c.texts(Stdout), ""))
	want, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Errorf("pwd = %q, want %q", got, want)
	}

	p, c = startHelper(t, "env", Spec{Env: []string{"RUNR_TEST_VALUE=hello world"}})
	waitDone(t, p, 5*time.Second)
	if got := c.texts(Stdout); !reflect.DeepEqual(got, []string{"hello world"}) {
		t.Errorf("env output = %q", got)
	}
}

func TestManyLinesInOrder(t *testing.T) {
	p, c := startHelper(t, "many", Spec{})
	waitDone(t, p, 10*time.Second)

	lines := c.texts(Stdout)
	if len(lines) != 10000 {
		t.Fatalf("got %d lines", len(lines))
	}
	for i, line := range lines {
		if line != strconv.Itoa(i) {
			t.Fatalf("line %d = %q", i, line)
		}
	}
}

func TestLineEdgeCases(t *testing.T) {
	tests := []struct {
		mode string
		want []string
	}{
		{"noeol", []string{"partial"}},
		{"crlf", []string{"a", "b"}},
		{"badutf8", []string{"a�b"}},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			p, c := startHelper(t, tt.mode, Spec{})
			waitDone(t, p, 5*time.Second)
			if got := c.texts(Stdout); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stdout = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLongLineIsSplit(t *testing.T) {
	p, c := startHelper(t, "longline", Spec{})
	waitDone(t, p, 5*time.Second)

	lines := c.texts(Stdout)
	total := 0
	for _, l := range lines {
		if len(l) > MaxLineBytes {
			t.Errorf("line of %d bytes exceeds MaxLineBytes", len(l))
		}
		total += len(l)
	}
	if len(lines) != 3 || total != 150_000 {
		t.Errorf("got %d lines, %d bytes total", len(lines), total)
	}
}

func TestStartInMissingDir(t *testing.T) {
	_, err := Start(Spec{Command: "true", Dir: filepath.Join(t.TempDir(), "gone")}, func(Line) {})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCommandNotFound(t *testing.T) {
	c := &collector{}
	p, err := Start(Spec{Command: "runr-no-such-command-xyz"}, c.add)
	if err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, p, 5*time.Second)
	if status.Success() {
		t.Errorf("status = %+v", status)
	}
	if len(c.texts(Stderr)) == 0 {
		t.Error("expected an error message on stderr")
	}
}

func TestExitStatusString(t *testing.T) {
	tests := map[string]ExitStatus{
		"exit code 0":        {Code: 0},
		"exit code 127":      {Code: 127},
		"signal: terminated": {Code: -1, Signal: "terminated"},
		"boom":               {Code: -1, Err: errors.New("boom")},
	}
	for want, s := range tests {
		if got := s.String(); got != want {
			t.Errorf("%+v.String() = %q, want %q", s, got, want)
		}
	}
}

func TestPid(t *testing.T) {
	p, c := startHelper(t, "sleep", Spec{})
	c.waitForPrefix(t, "ready")
	if p.Pid() <= 0 {
		t.Errorf("Pid() = %d", p.Pid())
	}
}

func TestPidsListsTree(t *testing.T) {
	p, c := startHelper(t, "grandchild", Spec{})
	line := c.waitForPrefix(t, "child ")
	child, err := strconv.Atoi(strings.TrimPrefix(line, "child "))
	if err != nil {
		t.Fatalf("bad child line %q", line)
	}
	other, oc := startHelper(t, "sleep", Spec{})
	oc.waitForPrefix(t, "ready")

	trees := Pids([]*Process{p, other})
	if len(trees) != 2 {
		t.Fatalf("Pids returned %d trees, want 2", len(trees))
	}
	if !slices.Contains(trees[0], p.Pid()) || !slices.Contains(trees[0], child) {
		t.Errorf("tree = %v, want the shell %d and the grandchild %d", trees[0], p.Pid(), child)
	}
	if !slices.Contains(trees[1], other.Pid()) || slices.Contains(trees[1], child) {
		t.Errorf("other tree = %v, want %d and not %d", trees[1], other.Pid(), child)
	}

	_ = p.Kill()
	waitDone(t, p, 5*time.Second)
	if pids := Pids([]*Process{p})[0]; len(pids) > 0 {
		t.Errorf("Pids after exit = %v, want none", pids)
	}
}
