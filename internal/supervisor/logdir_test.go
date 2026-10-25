//go:build !windows

package supervisor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/config"
)

func TestLogDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "broken.log"), 0o755); err != nil { // can't be written
		t.Fatal(err)
	}
	cfg := config.New()
	cfg.Settings.ShutdownTimeoutValue.Duration = testTimeout
	work := t.TempDir()
	cfg.Projects["api"] = &config.Project{Dir: work, Cmd: `printf '\033[32mhello\033[0m\n'; echo oops >&2; exit 3`}
	cfg.Projects["broken"] = &config.Project{Dir: work, Cmd: "echo lost"}
	s := New(cfg, Options{BaseEnv: []string{"PATH=" + os.Getenv("PATH")}, LogDir: dir})
	r := record(s)
	if err := s.Start("api", "broken"); err != nil {
		t.Fatal(err)
	}
	r.waitState(t, "api", Crashed)
	r.waitState(t, "broken", Exited)
	waitLog(t, s, "broken", "fail:can't write the log file, no more output is saved there: open "+
		filepath.Join(dir, "broken.log")+": is a directory")
	s.Close()

	b, err := os.ReadFile(filepath.Join(dir, "api.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d\.\d{3} `).ReplaceAllString(string(b), "T ")
	got = regexp.MustCompile(`session \d{4}-\d\d-\d\d \d\d:\d\d:\d\d`).ReplaceAllString(got, "session D")
	// stdout and stderr are read concurrently: either order.
	want1 := "--- runr session D ---\nT » starting: " + cfg.Projects["api"].Cmd + "\nT │ hello\nT │ oops\nT » crashed (exit code 3)\n"
	want2 := strings.Replace(want1, "T │ hello\nT │ oops", "T │ oops\nT │ hello", 1)
	if got != want1 && got != want2 {
		t.Errorf("api.log:\n%s\nwant:\n%s", got, want1)
	}
}
