package control

import (
	"os"
	"testing"
	"time"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

// The attach protocol with cmd.exe projects: lines, a restart from the
// view, and stopping the processes.
func TestAttachWindows(t *testing.T) {
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = 5 * time.Second
	cfg.Projects["api"] = &config.Project{Dir: t.TempDir(), Cmd: "echo api up & ping -n 60 127.0.0.1 >nul"}
	sup := supervisor.New(cfg, supervisor.Options{BaseEnv: os.Environ()})
	go func() {
		for range sup.Events() {
		}
	}()
	t.Cleanup(sup.Close)
	if err := sup.Start("api"); err != nil {
		t.Fatal(err)
	}
	srv, err := Listen(shortDir(t), sup, Info{Pid: 42, Titles: []string{"api"}, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	r, err := Attach(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	upLines := func() int {
		n := 0
		for _, l := range r.Logs().Lines("api") {
			if l.Text == "api up" || l.Text == "api up " {
				n++
			}
		}
		return n
	}
	waitUntil := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("no %s: %+v", what, r.Logs().Lines("api"))
			}
		}
	}
	waitUntil("first line", func() bool { return upLines() == 1 })
	if err := r.Restart("api"); err != nil {
		t.Fatal(err)
	}
	waitUntil("line after the restart", func() bool { return upLines() == 2 })

	r.StopAll() // without Info.Quit: the processes
	waitUntil("stop", func() bool {
		ps := r.Processes()
		return len(ps) == 1 && ps[0].State == supervisor.Stopped
	})
}
