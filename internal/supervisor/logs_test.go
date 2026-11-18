package supervisor

import (
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func texts(lines []LogLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func TestLogsAppend(t *testing.T) {
	l := NewLogs(10)
	fixed := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return fixed }

	a := l.Append("api", Stdout, "hello")
	b := l.Append("db", Stderr, "oops")
	c := l.Append("api", System, "exited with code 1")

	want := []LogLine{
		{Seq: 1, Project: "api", Stream: Stdout, Text: "hello", Time: fixed},
		{Seq: 3, Project: "api", Stream: System, Text: "exited with code 1", Time: fixed},
	}
	if got := l.Lines("api"); !reflect.DeepEqual(got, want) {
		t.Errorf("Lines(api) = %+v", got)
	}
	if a.Seq != 1 || b.Seq != 2 || c.Seq != 3 {
		t.Errorf("seqs = %d %d %d", a.Seq, b.Seq, c.Seq)
	}
	if got := l.Lines("nope"); got != nil {
		t.Errorf("Lines(nope) = %v", got)
	}
}

func TestLogsWraparound(t *testing.T) {
	l := NewLogs(3)
	for i := 1; i <= 3; i++ {
		l.Append("api", Stdout, strconv.Itoa(i))
	}
	if got := texts(l.Lines("api")); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Errorf("full: %v", got)
	}

	for i := 4; i <= 8; i++ {
		l.Append("api", Stdout, strconv.Itoa(i))
		want := []string{strconv.Itoa(i - 2), strconv.Itoa(i - 1), strconv.Itoa(i)}
		if got := texts(l.Lines("api")); !reflect.DeepEqual(got, want) {
			t.Errorf("after %d: %v, want %v", i, got, want)
		}
	}
}

func TestLogsSnapshotIsCopy(t *testing.T) {
	l := NewLogs(2)
	l.Append("api", Stdout, "a")
	snap := l.Lines("api")
	l.Append("api", Stdout, "b")
	l.Append("api", Stdout, "c")
	if snap[0].Text != "a" || len(snap) != 1 {
		t.Errorf("snapshot changed: %v", texts(snap))
	}
}

func TestLogsCombined(t *testing.T) {
	l := NewLogs(3)
	l.Append("api", Stdout, "api1")
	l.Append("db", Stdout, "db1")
	l.Append("api", Stdout, "api2")
	l.Append("web", Stdout, "web1")
	l.Append("db", Stdout, "db2")
	l.Append("api", Stdout, "api3")
	l.Append("api", Stdout, "api4") // evicts api1

	tests := []struct {
		projects []string
		want     []string
	}{
		{nil, []string{"db1", "api2", "web1", "db2", "api3", "api4"}},
		{[]string{"db", "api"}, []string{"db1", "api2", "db2", "api3", "api4"}},
		{[]string{"web", "missing"}, []string{"web1"}},
		{[]string{"missing"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.projects), func(t *testing.T) {
			if got := texts(l.Combined(tt.projects...)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Combined(%v) = %v, want %v", tt.projects, got, tt.want)
			}
		})
	}
}

func TestLogsMinimumCapacity(t *testing.T) {
	l := NewLogs(0)
	l.Append("api", Stdout, "a")
	l.Append("api", Stdout, "b")
	if got := texts(l.Lines("api")); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("got %v", got)
	}
}

func TestLogsConcurrent(t *testing.T) {
	const (
		writers = 8
		perW    = 2000
	)
	l := NewLogs(perW)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			project := fmt.Sprintf("p%d", w)
			for i := range perW {
				l.Append(project, Stdout, strconv.Itoa(i))
			}
		}()
		// Read concurrently with writes.
		go func() {
			_ = l.Combined()
		}()
	}
	wg.Wait()

	combined := l.Combined()
	if len(combined) != writers*perW {
		t.Fatalf("combined has %d lines, want %d", len(combined), writers*perW)
	}
	for i := 1; i < len(combined); i++ {
		if combined[i].Seq <= combined[i-1].Seq {
			t.Fatalf("combined not ordered at %d: %d after %d", i, combined[i].Seq, combined[i-1].Seq)
		}
	}
	for w := range writers {
		lines := l.Lines(fmt.Sprintf("p%d", w))
		for i, line := range lines {
			if line.Text != strconv.Itoa(i) {
				t.Fatalf("p%d line %d = %q", w, i, line.Text)
			}
		}
	}
}

// BenchmarkCombined measures merging 10 full 10k-line buffers, the worst
// case the combined view has to render from.
func BenchmarkCombined(b *testing.B) {
	l := NewLogs(10000)
	for i := range 100000 {
		l.Append(fmt.Sprintf("p%d", i%10), Stdout, "some log line")
	}
	b.ResetTimer()
	for b.Loop() {
		_ = l.Combined()
	}
}

func TestLogsSince(t *testing.T) {
	l := NewLogs(3)
	for i := range 5 { // api keeps 3 of its lines: a2 (seq 5), a3 (7), a4 (9)
		l.Append("api", Stdout, "a"+strconv.Itoa(i))
		l.Append("db", Stdout, "d"+strconv.Itoa(i))
	}
	cases := []struct {
		seq      uint64
		projects []string
		want     []string
	}{
		{0, nil, []string{"a2", "d2", "a3", "d3", "a4", "d4"}},
		{6, nil, []string{"a3", "d3", "a4", "d4"}},
		{6, []string{"api"}, []string{"a3", "a4"}},
		{9, []string{"db"}, []string{"d4"}},
		{10, nil, []string{}},
		{0, []string{"nope"}, []string{}},
	}
	for _, c := range cases {
		if got := texts(l.Since(c.seq, c.projects...)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Since(%d, %v) = %v, want %v", c.seq, c.projects, got, c.want)
		}
	}
}

func TestLogsChanged(t *testing.T) {
	l := NewLogs(10)
	ch := l.Changed()
	if l.Changed() != ch {
		t.Error("Changed returned a new channel before a line arrived")
	}
	select {
	case <-ch:
		t.Fatal("closed before a line arrived")
	default:
	}
	l.Append("api", Stdout, "hello")
	select {
	case <-ch:
	default:
		t.Fatal("not closed after a line arrived")
	}
	if next := l.Changed(); next == ch {
		t.Error("Changed returned the closed channel")
	}
	l.Append("api", Stdout, "nobody waits") // no channel to close
}
