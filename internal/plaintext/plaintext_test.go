package plaintext

import "testing"

func TestStripANSI(t *testing.T) {
	tests := map[string]string{
		"plain":                                  "plain",
		"\x1b[32mready\x1b[0m to accept":         "ready to accept",
		"\x1b[1;38;5;208mbold\x1b[m":             "bold",
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x07": "link",
		"a\x1b[2Kb\x1b[1Gc":                      "abc",
	}
	for in, want := range tests {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}
