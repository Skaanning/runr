package tui

import (
	"bytes"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// search is a plain-text search with smart case: a query without capital
// letters matches case-insensitively (ASCII letters only).
type search struct {
	query string
	bytes []byte // query, for searching byte slices
	fold  bool
}

func newSearch(query string) search {
	return search{query: query, bytes: []byte(query), fold: query == strings.ToLower(query)}
}

func (s search) active() bool { return s.query != "" }

// find returns the byte ranges of non-overlapping matches in plain text.
func (s search) find(text string) [][2]int {
	if s.query == "" {
		return nil
	}
	var out [][2]int
	for start := 0; ; {
		i := s.index(text[start:])
		if i < 0 {
			return out
		}
		i += start
		out = append(out, [2]int{i, i + len(s.query)})
		start = i + len(s.query)
	}
}

// matches reports whether a raw log line contains the query. buf is
// scratch space reused across calls.
func (s search) matches(raw string, buf *[]byte) bool {
	switch {
	case s.query == "":
		return false
	case isPlain(raw):
		return s.index(raw) >= 0
	}
	if b, ok := appendPlainFast((*buf)[:0], raw); ok {
		*buf = b
		if !s.fold {
			return bytes.Contains(b, s.bytes)
		}
		return indexFold(b, s.query) >= 0
	}
	return s.index(plain(sanitize(raw))) >= 0
}

func (s search) index(text string) int {
	if !s.fold {
		return strings.Index(text, s.query)
	}
	return indexFold(text, s.query)
}

// indexFold finds q, which has no capital letters, in text ignoring the case
// of ASCII letters.
func indexFold[T string | []byte](text T, q string) int {
	for i := 0; i+len(q) <= len(text); i++ {
		j := 0
		for j < len(q) && lowerASCII(text[i+j]) == q[j] {
			j++
		}
		if j == len(q) {
			return i
		}
	}
	return -1
}

// appendPlainFast appends plain(sanitize(raw)) to buf for the common case of
// ASCII text with CSI sequences, without decoding it grapheme by grapheme.
// It reports false for anything else (tabs, other controls or escapes,
// non-ASCII), which needs the full treatment.
func appendPlainFast(buf []byte, raw string) ([]byte, bool) {
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == ansi.ESC:
			if i+1 >= len(raw) || raw[i+1] != '[' {
				return buf, false
			}
			j := i + 2
			for j < len(raw) && raw[j] >= 0x20 && raw[j] <= 0x3f { // parameters, intermediates
				j++
			}
			if j >= len(raw) || raw[j] < 0x40 || raw[j] > 0x7e { // final byte
				return buf, false
			}
			i = j
		case c < 0x20 || c >= 0x7f:
			return buf, false
		default:
			buf = append(buf, c)
		}
	}
	return buf, true
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// plain returns sanitized text without its SGR sequences.
func plain(s string) string {
	if isPlain(s) {
		return s
	}
	var b strings.Builder
	var state byte
	for len(s) > 0 {
		seq, _, n, newState := ansi.DecodeSequence(s, state, nil)
		state = newState
		s = s[n:]
		if seq[0] != ansi.ESC {
			b.WriteString(seq)
		}
	}
	return b.String()
}

// highlight marks the given byte ranges of plain(s) in sanitized text s with
// the SGR sequence on. After each match the line's own styling is restored.
func highlight(s string, ranges [][2]int, on string) string {
	var b strings.Builder
	var style strings.Builder // the line's styling since its last reset
	pos, r := 0, 0            // position in the plain text; next range
	in := false
	var state byte
	for len(s) > 0 && r < len(ranges) {
		seq, _, n, newState := ansi.DecodeSequence(s, state, nil)
		state = newState
		s = s[n:]
		if seq[0] == ansi.ESC {
			if isReset(seq) {
				style.Reset()
			}
			if seq != sgrReset && seq != "\x1b[m" {
				style.WriteString(seq)
			}
			b.WriteString(seq)
			if in {
				b.WriteString(on) // the line's own styling must not end the highlight
			}
			continue
		}
		if !in && pos+len(seq) > ranges[r][0] {
			b.WriteString(on)
			in = true
		}
		b.WriteString(seq)
		pos += len(seq)
		if in && pos >= ranges[r][1] {
			b.WriteString(sgrReset)
			b.WriteString(style.String())
			in = false
			for r < len(ranges) && ranges[r][1] <= pos {
				r++
			}
		}
	}
	b.WriteString(s)
	return b.String()
}
