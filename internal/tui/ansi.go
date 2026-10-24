package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const tabWidth = 8

// sanitize prepares a line of program output for display. SGR sequences
// (colors and text attributes) are kept; every other escape sequence and
// control character is removed, since cursor movement or screen clearing
// would break the layout. Tabs are expanded, and a carriage return keeps only
// the text after it, like a terminal overwriting a progress line.
func sanitize(s string) string {
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	if isPlain(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	col := 0
	var state byte
	for len(s) > 0 {
		seq, width, n, newState := ansi.DecodeSequence(s, state, nil)
		state = newState
		s = s[n:]
		switch {
		case seq == "\t":
			spaces := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", spaces))
			col += spaces
		case seq[0] == ansi.ESC || seq[0] < 0x20 || seq[0] == 0x7f:
			if isSGR(seq) {
				b.WriteString(seq)
			}
		case width == 0 && isControl(seq):
			// C1 controls and the like, encoded as UTF-8.
		default:
			b.WriteString(seq)
			col += width
		}
	}
	return b.String()
}

// isPlain reports whether s is printable ASCII, which needs no sanitizing.
func isPlain(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

// isSGR reports whether seq is a CSI "select graphic rendition" sequence
// such as "\x1b[1;31m".
func isSGR(seq string) bool {
	if len(seq) < 3 || !strings.HasPrefix(seq, "\x1b[") || seq[len(seq)-1] != 'm' {
		return false
	}
	for _, c := range seq[2 : len(seq)-1] {
		if (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}

func isControl(seq string) bool {
	r, _ := utf8.DecodeRuneInString(seq)
	return unicode.IsControl(r)
}

// isReset reports whether an SGR sequence starts by resetting all attributes.
func isReset(seq string) bool {
	params := seq[2 : len(seq)-1]
	return params == "" || params == "0" || strings.HasPrefix(params, "0;")
}

const sgrReset = "\x1b[0m"

// wrap splits a sanitized line into rows of at most width cells. Every row
// that uses styling ends with a reset, and continuation rows start by
// re-applying the styling active at the break, so each row renders correctly
// on its own. An empty line yields one empty row.
func wrap(s string, width int) []string {
	width = max(width, 1)
	if isPlain(s) && len(s) <= width {
		return []string{s}
	}

	var rows []string
	var row strings.Builder
	var style strings.Builder // SGR sequences in effect, since the last reset
	col := 0
	styled := false // whether row contains or inherited styling
	var state byte
	endRow := func() {
		if styled {
			row.WriteString(sgrReset)
		}
		rows = append(rows, row.String())
		row.Reset()
		row.WriteString(style.String())
		col = 0
		styled = style.Len() > 0
	}
	for len(s) > 0 {
		seq, w, n, newState := ansi.DecodeSequence(s, state, nil)
		state = newState
		s = s[n:]
		if seq[0] == ansi.ESC {
			if isReset(seq) {
				style.Reset()
			}
			if seq != sgrReset && seq != "\x1b[m" {
				style.WriteString(seq)
			}
			row.WriteString(seq)
			styled = true
			continue
		}
		if col+w > width && col > 0 {
			endRow()
		}
		row.WriteString(seq)
		col += w
	}
	if styled {
		row.WriteString(sgrReset)
	}
	return append(rows, row.String())
}
