package main

import (
	"regexp"
	"runtime"
	"strings"
)

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// joinCommand turns the arguments given after "--" back into a single command
// line for the system shell, quoting arguments that need it.
func joinCommand(args []string) string {
	quote := quotePOSIX
	if runtime.GOOS == "windows" {
		quote = quoteWindows
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quote(a)
	}
	return strings.Join(quoted, " ")
}

func quotePOSIX(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func quoteWindows(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"&|<>^") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
