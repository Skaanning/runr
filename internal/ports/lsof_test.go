package ports

import (
	"slices"
	"testing"
)

func TestParseLsof(t *testing.T) {
	out := []byte(`p501
f23
n*:8080
f24
n[::1]:8080
p777
f5
n127.0.0.1:5173
f6
n[::]:24678
p900
f7
n127.0.0.1:5000->127.0.0.1:61000
f8
nweird
`)
	got := tidy(parseLsof(out, nil))
	want := []Listener{{501, 8080}, {777, 5173}, {777, 24678}}
	if !slices.Equal(got, want) {
		t.Errorf("all = %v, want %v", got, want)
	}
	got = tidy(parseLsof(out, map[int]bool{777: true}))
	if want := want[1:]; !slices.Equal(got, want) {
		t.Errorf("pid 777 = %v, want %v", got, want)
	}
	if got := parseLsof(nil, nil); len(got) != 0 {
		t.Errorf("empty output = %v", got)
	}
}
