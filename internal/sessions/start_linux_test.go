package sessions

import "testing"

func TestStatStart(t *testing.T) {
	stat := "4123 (node (dev)) S 1 4123 4123 0 -1 4194560 1 0 0 0 5 1 0 0 20 0 7 0 987654 1234 56"
	if got, err := statStart([]byte(stat)); err != nil || got != "987654" {
		t.Errorf("statStart = %q, %v", got, err)
	}
	if _, err := statStart([]byte("1 (x) S 1")); err == nil {
		t.Error("short stat parsed")
	}
}
