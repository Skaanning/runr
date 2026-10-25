package proc

import "testing"

func TestStatPgid(t *testing.T) {
	for _, tc := range []struct {
		stat string
		want int
	}{
		{"1234 (node) S 1 1234 1234 0 -1 4194304", 1234},
		{"42 (a (b) c) R 7 99 99 0", 99}, // parentheses and spaces in the name
		{"42 (x)", -1},
		{"garbage", -1},
	} {
		if got := statPgid([]byte(tc.stat)); got != tc.want {
			t.Errorf("statPgid(%q) = %d, want %d", tc.stat, got, tc.want)
		}
	}
}
