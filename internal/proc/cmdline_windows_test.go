//go:build windows

package proc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my code")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := CommandLine(dir, []string{"RUNR_TEST_A=1", "RUNR_TEST_B=hello world"}, "echo [%RUNR_TEST_B%] [%RUNR_TEST_A%] [%CD%]")
	want := `cd /d "` + dir + `" && set "RUNR_TEST_A=1" && set "RUNR_TEST_B=hello world" && cmd /d /s /c "echo [%RUNR_TEST_B%] [%RUNR_TEST_A%] [%CD%]"`
	if line != want {
		t.Fatalf("line:\n%s\nwant:\n%s", line, want)
	}

	// Run as runr runs commands: the line sets the variables for the command
	// and runs it in dir.
	out, err := newCommand(line).Output()
	if err != nil {
		t.Fatal(err)
	}
	// The temp dir may show up as a short (8.3) path; its last part doesn't.
	if got := strings.TrimSpace(string(out)); !strings.HasPrefix(got, "[hello world] [1] [") || !strings.HasSuffix(got, `\my code]`) {
		t.Fatalf("output = %q", got)
	}
}
