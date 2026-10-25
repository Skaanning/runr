// Package plaintext turns terminal output into plain text.
package plaintext

import "regexp"

// ansiPattern matches CSI sequences (colors, cursor movement) and OSC
// sequences (e.g. hyperlinks, window titles).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// StripANSI removes ANSI escape sequences from s.
func StripANSI(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			return ansiPattern.ReplaceAllString(s, "")
		}
	}
	return s
}
