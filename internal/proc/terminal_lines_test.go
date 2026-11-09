package proc

import (
	"io"
	"slices"
	"testing"
)

func TestEditLine(t *testing.T) {
	for in, want := range map[string]string{
		"plain line":                 "plain line",
		"\x1b[32mgreen\x1b[0m":       "\x1b[32mgreen\x1b[0m",
		"branch: feature/xx\b \b":    "branch: feature/x",
		"abc\b\bX":                   "aXc",
		"10%\r20%\r30%":              "30%",
		"loading...\rdone\x1b[K":     "done",
		"abcdef\x1b[3D\x1b[K":        "abc",
		"ab\x1b[2Cc":                 "ab  c",
		"abcdef\x1b[2GX":             "aXcdef",
		"x\x1b[2K\rnew":              "new",
		"\x1b[31mred\b\b\x1b[32mEE":  "\x1b[31mr\x1b[32mEE",
		"prompt> \x1b[1mbold\x1b[0m": "prompt> \x1b[1mbold\x1b[0m",
	} {
		if got := editLine(in); got != want {
			t.Errorf("editLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// chunks is a reader that returns one chunk per Read.
type chunks []string

func (c *chunks) Read(b []byte) (int, error) {
	if len(*c) == 0 {
		return 0, io.EOF
	}
	n := copy(b, (*c)[0])
	*c = (*c)[1:]
	return n, nil
}

func TestReadTerminal(t *testing.T) {
	r := &chunks{"first\r\nbran", "ch: ", "feat\xc3", "\xa9\r\n", "\x1b[3", "2mgreen\x1b[0m\r\nlast"}
	var got []string
	readTerminal(r, func(l Line) {
		if l.Partial {
			got = append(got, "~"+l.Text)
		} else {
			got = append(got, l.Text)
		}
	})
	want := []string{
		"first", "~bran", "~branch: ", "~branch: feat", // the cut-off é waits
		"branch: featé",
		// "\x1b[3" alone is a cut-off escape sequence: nothing to show
		"\x1b[32mgreen\x1b[0m", "~last",
		"last", // at the end, the unfinished line is complete
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
