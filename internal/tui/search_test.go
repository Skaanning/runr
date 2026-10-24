package tui

import (
	"fmt"
	"slices"
	"testing"

	"runr/internal/supervisor"
)

func TestSearchFind(t *testing.T) {
	tests := []struct {
		query, text string
		want        [][2]int
	}{
		{"err", "no match here", nil},
		{"err", "error: err", [][2]int{{0, 3}, {7, 10}}},
		{"err", "ERROR Err", [][2]int{{0, 3}, {6, 9}}}, // lowercase query folds
		{"Err", "ERROR Err err", [][2]int{{6, 9}}},     // capitals: exact case
		{"aa", "aaaaa", [][2]int{{0, 2}, {2, 4}}},      // non-overlapping
		{"世界", "hello 世界", [][2]int{{6, 12}}},
		{"", "anything", nil},
	}
	for _, tt := range tests {
		got := newSearch(tt.query).find(tt.text)
		if !slices.Equal(got, tt.want) {
			t.Errorf("find(%q, %q) = %v, want %v", tt.query, tt.text, got, tt.want)
		}
	}
}

func TestSearchMatchesIgnoresEscapes(t *testing.T) {
	var buf []byte
	s := newSearch("error")
	if !s.matches("\x1b[31mERR\x1b[0mOR", &buf) {
		t.Error("styling inside the word should not prevent a match")
	}
	if s.matches("\x1b[2Kerr\tor", &buf) {
		t.Error("matched across a tab")
	}
	if !newSearch("ERROR").matches("\x1b[1mERROR\x1b[0m", &buf) {
		t.Error("exact-case match failed")
	}
}

func TestAppendPlainFastAgreesWithPlain(t *testing.T) {
	inputs := []string{
		"plain",
		"\x1b[1;31mred\x1b[0m text",
		"\x1b[2K\x1b[1Gprogress 50%",
		"\x1b[?25lhidden\x1b[?25h",
		"\x1b[38:5:208mx\x1b[m",
		"\x1b[31", // unterminated
		"\x1b]0;title\x07x",
		"tab\there",
		"héllo \x1b[1mwörld",
		"a\rb",
	}
	for _, in := range inputs {
		got, ok := appendPlainFast(nil, in)
		if !ok {
			continue
		}
		if want := plain(sanitize(in)); string(got) != want {
			t.Errorf("appendPlainFast(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range inputs[6:] {
		if _, ok := appendPlainFast(nil, in); ok {
			t.Errorf("appendPlainFast(%q) took the fast path", in)
		}
	}
}

func TestPlain(t *testing.T) {
	if got := plain("\x1b[1;31mred\x1b[0m text"); got != "red text" {
		t.Fatalf("plain = %q", got)
	}
}

func TestHighlight(t *testing.T) {
	const on, off = "<", "\x1b[0m"
	tests := []struct {
		name, in string
		ranges   [][2]int
		want     string
	}{
		{"plain", "an error here", [][2]int{{3, 8}}, "an <error" + off + " here"},
		{"two", "aXbX", [][2]int{{1, 2}, {3, 4}}, "a<X" + off + "b<X" + off},
		{"adjacent", "aa", [][2]int{{0, 1}, {1, 2}}, "<a" + off + "<a" + off},
		{
			"restores line style",
			"\x1b[32mok error ok", [][2]int{{3, 8}},
			"\x1b[32mok <error" + off + "\x1b[32m ok",
		},
		{
			"style change inside match",
			"e\x1b[31mrr\x1b[0mor x", [][2]int{{0, 5}},
			"<e\x1b[31m<rr\x1b[0m<or" + off + " x",
		},
		{"wide chars", "a世界b", [][2]int{{1, 7}}, "a<世界" + off + "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := highlight(tt.in, tt.ranges, on); got != tt.want {
				t.Errorf("highlight = %q, want %q", got, tt.want)
			}
		})
	}
}

func BenchmarkMatching100k(b *testing.B) {
	v := newLogView(100000, plainFormat)
	lines := make([]supervisor.LogLine, 100000)
	for i := range lines {
		text := fmt.Sprintf("2026-10-24T10:00:00Z INFO request handled method=GET path=/users/%d status=200", i)
		if i%2 == 0 {
			text = "\x1b[32m" + text + "\x1b[0m"
		}
		lines[i] = supervisor.LogLine{Seq: uint64(i + 1), Text: text}
	}
	v.append(lines...)
	s := newSearch("status=500")
	for b.Loop() {
		v.matching(s)
	}
}
