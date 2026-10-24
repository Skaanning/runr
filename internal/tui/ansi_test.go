package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "hello world", "hello world"},
		{"sgr kept", "\x1b[1;31merror\x1b[0m done", "\x1b[1;31merror\x1b[0m done"},
		{"sgr colon params", "\x1b[38:5:208mx", "\x1b[38:5:208mx"},
		{"cursor movement dropped", "a\x1b[2Kb\x1b[1Ac", "abc"},
		{"private mode dropped", "\x1b[?25lhidden", "hidden"},
		{"osc dropped", "\x1b]0;title\x07text", "text"},
		{"hyperlink text kept", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"esc sequence dropped", "a\x1b7b\x1b8c", "abc"},
		{"control chars dropped", "a\bb\x07c\x00d\x7fe", "abcde"},
		{"tab expanded", "a\tb", "a       b"},
		{"tab after wide char", "日\tx", "日      x"},
		{"tab ignores sgr width", "\x1b[31mab\x1b[0m\tc", "\x1b[31mab\x1b[0m      c"},
		{"carriage return keeps last part", "10%\r20%\r30% done", "30% done"},
		{"unicode kept", "héllo 👋 世界", "héllo 👋 世界"},
		{"c1 control dropped", "a\u009bb", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitize(tt.in); got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"empty", "", 5, []string{""}},
		{"fits", "abc", 5, []string{"abc"}},
		{"exact", "abcde", 5, []string{"abcde"}},
		{"split", "abcdefghij12", 5, []string{"abcde", "fghij", "12"}},
		{"wide chars don't split", "ab世界", 3, []string{"ab", "世", "界"}},
		{
			"style carried to next row",
			"\x1b[31mabcdef\x1b[0mgh", 4,
			[]string{"\x1b[31mabcd\x1b[0m", "\x1b[31mef\x1b[0mgh\x1b[0m"},
		},
		{
			"reset clears carried style",
			"\x1b[1mab\x1b[0mcdef", 4,
			[]string{"\x1b[1mab\x1b[0mcd\x1b[0m", "ef"},
		},
		{
			"styles accumulate",
			"\x1b[1m\x1b[32mabcde", 3,
			[]string{"\x1b[1m\x1b[32mabc\x1b[0m", "\x1b[1m\x1b[32mde\x1b[0m"},
		},
		{"zero width", "abc", 0, []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrap(tt.in, tt.width)
			if !slices.Equal(got, tt.want) {
				t.Errorf("wrap(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
			for _, row := range got {
				if w := ansi.StringWidth(row); w > max(tt.width, 1) && ansi.StringWidth(ansi.Strip(row)) > 1 {
					t.Errorf("row %q is %d cells wide, limit %d", row, w, tt.width)
				}
			}
		})
	}
}

func BenchmarkSanitizeColored(b *testing.B) {
	line := "\x1b[32mINFO\x1b[0m 2026-10-24T10:00:00Z request handled method=GET path=/users status=200 dur=2ms"
	for b.Loop() {
		sanitize(line)
	}
}

func BenchmarkWrapLong(b *testing.B) {
	line := sanitize("\x1b[33m" + strings.Repeat("lorem ipsum dolor sit amet ", 400))
	for b.Loop() {
		wrap(line, 120)
	}
}
