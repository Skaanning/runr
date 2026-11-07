package proc

import "strings"

// keepColors removes the escape sequences from s that aren't colors (SGR,
// ESC [ … m): cursor movement, erasing, modes, window titles. ConPTY renders
// a screen rather than passing a program's output through, and a log line
// keeps only what it says and how it is colored.
func keepColors(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			i++
			continue
		}
		n, color := escapeSequence(s[i:])
		if color {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// consoleLine returns a line of a pseudo console's (ConPTY's) output as runr
// keeps it: its colors (keepColors), without the color resets it starts
// lines with. Each log line starts out uncolored anyway.
func consoleLine(s string) string {
	s = keepColors(s)
	for {
		switch {
		case strings.HasPrefix(s, "\x1b[m"):
			s = s[3:]
		case strings.HasPrefix(s, "\x1b[0m"):
			s = s[4:]
		default:
			return s
		}
	}
}

// escapeSequence returns the length of the escape sequence s starts with,
// and whether it sets colors. An unfinished one takes the rest of s.
func escapeSequence(s string) (n int, color bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch s[1] {
	case '[': // CSI: parameters, intermediates, a final byte
		i := 2
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		params := s[2:i]
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i >= len(s) {
			return len(s), false
		}
		return i + 1, s[i] == 'm' && i == 2+len(params) && strings.Trim(params, "0123456789;:") == ""
	case ']', 'P', '_', '^': // OSC, DCS, APC, PM: up to BEL or ST (ESC \)
		for i := 2; i < len(s); i++ {
			if s[i] == '\a' {
				return i + 1, false
			}
			if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2, false
			}
		}
		return len(s), false
	}
	return 2, false
}
