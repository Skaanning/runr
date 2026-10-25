//go:build !windows

package proc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "my code")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"FORCE_COLOR=1", "QUOTE=it's \"x\" $HOME", "EMPTY="}
	line := CommandLine(dir, env, `printf '%s|%s|%s|%s' "$PWD" "$FORCE_COLOR" "$QUOTE" "${EMPTY-unset}"`)

	want := `cd ~/'my code' && env FORCE_COLOR=1 QUOTE='it'\''s "x" $HOME' EMPTY='' /bin/sh -c ` +
		`'printf '\''%s|%s|%s|%s'\'' "$PWD" "$FORCE_COLOR" "$QUOTE" "${EMPTY-unset}"'`
	if line != want {
		t.Fatalf("line:\n%s\nwant:\n%s", line, want)
	}

	// Run as runr runs commands: the line gives the same directory, variables
	// and command.
	out, err := newCommand(line).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), dir+`|1|it's "x" $HOME|`; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	// Outside the home directory the path is quoted as it is.
	if got := CommandLine("/srv/app", nil, "make"); got != "cd /srv/app && /bin/sh -c make" {
		t.Fatalf("line = %q", got)
	}
}
