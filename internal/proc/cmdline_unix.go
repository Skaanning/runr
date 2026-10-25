//go:build !windows

package proc

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CommandLine returns one line for a POSIX shell that runs command the way
// runr does: in dir, with env (KEY=VALUE pairs) added to the environment, by
// /bin/sh. A dir in the home directory is written with ~.
func CommandLine(dir string, env []string, command string) string {
	var b strings.Builder
	b.WriteString("cd " + shellPath(dir) + " && ")
	if len(env) > 0 {
		b.WriteString("env ")
		for _, kv := range env {
			if k, v, _ := strings.Cut(kv, "="); safeShellWord.MatchString(k) {
				b.WriteString(k + "=" + shellQuote(v) + " ")
			} else {
				b.WriteString(shellQuote(kv) + " ")
			}
		}
	}
	b.WriteString("/bin/sh -c " + shellQuote(command))
	return b.String()
}

// safeShellWord matches words a POSIX shell takes literally.
var safeShellWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell, unless it needs no quotes.
func shellQuote(s string) string {
	if safeShellWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellPath quotes path, writing the home directory as ~ (left unquoted, so
// the shell expands it).
func shellPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return shellQuote(path)
	}
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + shellQuote(rel)
	}
	return shellQuote(path)
}
