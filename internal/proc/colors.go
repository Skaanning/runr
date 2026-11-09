package proc

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

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

// escapeComplete reports whether b starts with a whole escape sequence, not
// one cut off at its end.
func escapeComplete(b []byte) bool {
	s := string(b)
	if len(s) < 2 {
		return false
	}
	switch s[1] {
	case '[':
		i := 2
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
			i++
		}
		return i < len(s)
	case ']', 'P', '_', '^':
		return strings.HasSuffix(s, "\a") || strings.HasSuffix(s, "\x1b\\")
	}
	return true
}

// editLine replays the editing a program does within a line on a terminal:
// carriage returns and backspaces, erasing to the end of the line and moving
// the cursor along it (ESC [ K, C, D, G), as when typed text is echoed,
// corrected with backspace, or a progress bar is redrawn. The line is what
// the terminal would show. Other escape sequences (colors) stay with the
// text after them. A line without such editing is returned as it is.
func editLine(s string) string {
	if !strings.ContainsAny(s, "\r\b\x1b") || !hasEditing(s) {
		return s
	}
	type cell struct {
		pre string // escape sequences before the character
		ch  string
	}
	var cells []cell
	var pre strings.Builder // escape sequences waiting for the next character
	col := 0
	put := func(ch string) {
		for len(cells) < col {
			cells = append(cells, cell{ch: " "})
		}
		c := cell{pre: pre.String(), ch: ch}
		pre.Reset()
		if col < len(cells) {
			cells[col] = c
		} else {
			cells = append(cells, c)
		}
		col++
	}
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\r':
			col = 0
			i++
		case c == '\b':
			col = max(col-1, 0)
			i++
		case c == '\x1b':
			n, _ := escapeSequence(s[i:])
			seq := s[i : i+n]
			i += n
			if final, params, ok := csi(seq); ok && strings.ContainsRune("KCDG", rune(final)) {
				arg := func(k, def int) int {
					fields := strings.Split(params, ";")
					if k < len(fields) {
						if v, err := strconv.Atoi(fields[k]); err == nil && v > 0 {
							return v
						}
					}
					return def
				}
				switch final {
				case 'K':
					switch arg(0, 0) {
					case 0:
						if col < len(cells) {
							cells = cells[:col]
						}
					case 1:
						for j := 0; j <= col && j < len(cells); j++ {
							cells[j] = cell{ch: " "}
						}
					case 2:
						cells = cells[:0]
					}
				case 'C':
					col += arg(0, 1)
				case 'D':
					col = max(col-arg(0, 1), 0)
				case 'G':
					col = arg(0, 1) - 1
				}
				continue
			}
			pre.WriteString(seq)
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			put(s[i : i+size])
			i += size
		}
	}
	var b strings.Builder
	for _, c := range cells {
		b.WriteString(c.pre)
		b.WriteString(c.ch)
	}
	return strings.TrimRight(b.String(), " ") + pre.String()
}

// hasEditing reports whether s has any of the editing editLine replays.
func hasEditing(s string) bool {
	if strings.ContainsAny(s, "\r\b") {
		return true
	}
	for i := strings.IndexByte(s, '\x1b'); i >= 0; {
		n, _ := escapeSequence(s[i:])
		if final, _, ok := csi(s[i : i+n]); ok && strings.ContainsRune("KCDG", rune(final)) {
			return true
		}
		j := strings.IndexByte(s[i+n:], '\x1b')
		if j < 0 {
			break
		}
		i += n + j
	}
	return false
}

// csi returns the final byte and the parameters of a complete control
// sequence (ESC [ params final) without intermediates or private markers.
func csi(seq string) (final byte, params string, ok bool) {
	if len(seq) < 3 || seq[1] != '[' {
		return 0, "", false
	}
	params, final = seq[2:len(seq)-1], seq[len(seq)-1]
	if strings.Trim(params, "0123456789;") != "" || final < 0x40 || final > 0x7e {
		return 0, "", false
	}
	return final, params, true
}
