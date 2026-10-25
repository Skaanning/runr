package ports

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/proc"
)

// helperEnv makes the test binary listen on a port instead of running
// tests: it prints "port <n>" and waits.
const helperEnv = "RUNR_PORTS_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println("port", l.Addr().(*net.TCPAddr).Port)
		time.Sleep(time.Hour)
	}
	os.Exit(m.Run())
}

// listen opens a listener on a random port of 127.0.0.1 (v6: ::1).
func listen(t *testing.T, network, addr string) int {
	t.Helper()
	l, err := net.Listen(network, addr)
	if err != nil {
		t.Skipf("can't listen on %s: %v", addr, err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// lookup calls Listeners, skipping the test where ports aren't available.
func lookup(t *testing.T, pids []int) []Listener {
	t.Helper()
	ls, err := Listeners(pids)
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

func TestListenersOwnProcess(t *testing.T) {
	v4 := listen(t, "tcp4", "127.0.0.1:0")
	pid := os.Getpid()
	want := []Listener{{pid, v4}}
	if l, err := net.Listen("tcp6", "[::1]:0"); err == nil {
		defer l.Close()
		want = append(want, Listener{pid, l.Addr().(*net.TCPAddr).Port})
	}
	slices.SortFunc(want, func(a, b Listener) int { return a.Port - b.Port })

	if got := lookup(t, []int{pid}); !slices.Equal(got, want) {
		t.Errorf("Listeners(own pid) = %v, want %v", got, want)
	}
	if got := lookup(t, nil); !slices.Contains(got, want[0]) {
		t.Errorf("Listeners(nil) = %v, missing %v", got, want[0])
	}
	if got := lookup(t, []int{}); len(got) != 0 {
		t.Errorf("Listeners(no pids) = %v, want none", got)
	}
}

func TestListenersSamePortTwice(t *testing.T) {
	// A port open on several addresses of one process is listed once.
	port := listen(t, "tcp4", "127.0.0.1:0")
	l, err := net.Listen("tcp6", "[::1]:"+strconv.Itoa(port))
	if err != nil {
		t.Skipf("can't listen on [::1]:%d: %v", port, err)
	}
	defer l.Close()
	if got, want := lookup(t, []int{os.Getpid()}), []Listener{{os.Getpid(), port}}; !slices.Equal(got, want) {
		t.Errorf("Listeners = %v, want %v", got, want)
	}
}

func TestListenersProcessTree(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	cmd := "'" + strings.ReplaceAll(os.Args[0], "'", `'\''`) + "'"
	if runtime.GOOS == "windows" {
		cmd = `"` + os.Args[0] + `"`
	}
	p, err := proc.Start(proc.Spec{
		Command: cmd,
		Env:     proc.BuildEnv(os.Environ(), map[string]string{helperEnv: "1", "GORACE": "atexit_sleep_ms=0"}),
	}, func(l proc.Line) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, l.Text)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.Kill()
		<-p.Done()
	})

	port := 0
	for deadline := time.Now().Add(5 * time.Second); port == 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		for _, l := range lines {
			if s, ok := strings.CutPrefix(l, "port "); ok {
				port, _ = strconv.Atoi(s)
			}
		}
		mu.Unlock()
	}
	if port == 0 {
		t.Fatalf("helper didn't report its port; output %q", lines)
	}

	pids := proc.Pids([]*proc.Process{p})[0]
	ls := lookup(t, pids)
	i := slices.IndexFunc(ls, func(l Listener) bool { return l.Port == port })
	if i < 0 {
		t.Fatalf("Listeners(%v) = %v, missing port %d", pids, ls, port)
	}
	if name := Name(ls[i].Pid); !strings.HasPrefix(name, "ports.test") {
		t.Errorf("Name(%d) = %q, want the test binary", ls[i].Pid, name)
	}
	// Other processes' listeners are not included.
	own := listen(t, "tcp4", "127.0.0.1:0")
	if slices.ContainsFunc(lookup(t, pids), func(l Listener) bool { return l.Port == own }) {
		t.Errorf("the helper's listeners include the test's own port %d", own)
	}
}

func TestName(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("no process names here")
	}
	if name := Name(os.Getpid()); !strings.HasPrefix(name, "ports.test") {
		t.Errorf("Name(own pid) = %q, want the test binary", name)
	}
	if name := Name(1 << 30); name != "" {
		t.Errorf("Name(no such pid) = %q", name)
	}
}
