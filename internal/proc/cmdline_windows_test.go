//go:build windows

package proc

import (
	"strings"
	"testing"
)

func TestCommandLine(t *testing.T) {
	dir := t.TempDir()
	line := CommandLine(dir, []string{"FORCE_COLOR=1", "GREETING=hello world"}, "echo %GREETING%|%FORCE_COLOR%")
	want := `cd /d "` + dir + `" && set "FORCE_COLOR=1" && set "GREETING=hello world" && cmd /d /s /c "echo %GREETING%|%FORCE_COLOR%"`
	if line != want {
		t.Fatalf("line:\n%s\nwant:\n%s", line, want)
	}

	// Run as runr runs commands: the line sets the variables for the command.
	out, err := newCommand(line).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "hello world|1" {
		t.Fatalf("output = %q", got)
	}
}
