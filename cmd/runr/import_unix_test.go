//go:build !windows

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDockerEnv makes the test binary act like `docker compose up ...
// <service>`: it says so, listens on $RUNR_TEST_PORT_<service> if set, and
// exits after a moment.
const fakeDockerEnv = "RUNR_TEST_FAKE_DOCKER"

func fakeDocker() {
	service := os.Args[len(os.Args)-1]
	if port := os.Getenv("RUNR_TEST_PORT_" + service); port != "" {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			fmt.Println("listen:", err)
			os.Exit(1)
		}
		defer ln.Close()
	}
	fmt.Println("fake", strings.Join(os.Args[1:], " "))
	time.Sleep(700 * time.Millisecond)
	os.Exit(0)
}

// TestImportedGroupStarts imports the fixtures and runs their groups: the
// compose services start in dependency order, each once the one before it
// listens on its port.
func TestImportedGroupStarts(t *testing.T) {
	testEnv(t)
	for _, port := range []string{"47811", "47812"} {
		if ln, err := net.Listen("tcp", "127.0.0.1:"+port); err != nil {
			t.Skipf("port %s is taken: %v", port, err)
		} else {
			ln.Close()
		}
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nexec '" + os.Args[0] + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeDockerEnv, "1")
	t.Setenv("RUNR_TEST_PORT_db", "47811")
	t.Setenv("RUNR_TEST_PORT_api", "47812")

	mustRun(t, "import", fixture(t, "compose.yaml"))
	mustRun(t, "import", fixture(t, "Procfile"), "--group", "procs", "--prefix", "proc-")

	out := mustRun(t, "up", "--plain", "shop")
	var order []string
	for _, line := range strings.Split(out, "\n") {
		for _, event := range []string{"starting: docker compose up", "ready"} {
			if strings.Contains(line, event) {
				order = append(order, strings.Fields(line)[0]+" "+event)
			}
		}
	}
	want := []string{
		"db starting: docker compose up", "db ready",
		"api starting: docker compose up", "api ready",
		"web starting: docker compose up",
	}
	if strings.Join(order, "\n") != strings.Join(want, "\n") {
		t.Fatalf("order:\n%s\nwant:\n%s\noutput:\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"), out)
	}
	for _, svc := range []string{"db", "api", "web"} {
		if !strings.Contains(out, "fake compose up --no-deps --no-log-prefix "+svc) {
			t.Errorf("%s didn't run:\n%s", svc, out)
		}
	}

	out = mustRun(t, "up", "--plain", "procs")
	for _, want := range []string{"proc-web", "web up", "proc-worker", "worker up"} {
		if !strings.Contains(out, want) {
			t.Errorf("procs output lacks %q:\n%s", want, out)
		}
	}
}
