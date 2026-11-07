// Package state keeps what runr remembers between runs that isn't
// configuration: the last session, which `runr up --last` and the
// launcher's l start again. Errors writing it are ignored; it only helps.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Skaanning/runr/internal/config"
)

// EnvVar overrides the state directory, e.g. for tests.
const EnvVar = "RUNR_STATE_DIR"

// lastFile is the last session's file in the state directory.
const lastFile = "last-session.json"

// Dir is runr's state directory: $RUNR_STATE_DIR if set, otherwise
// $XDG_STATE_HOME/runr (default ~/.local/state/runr) on Linux and other
// Unix systems, runr in the user cache directory on macOS and Windows
// (%LocalAppData%\runr).
func Dir() (string, error) {
	if d := os.Getenv(EnvVar); d != "" {
		return filepath.Abs(d)
	}
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "runr"), nil
}

// LastSession is what the most recent session started: groups and
// projects in the order they were started, and extra arguments by project.
type LastSession struct {
	Names []string          `json:"names"`
	Args  map[string]string `json:"args,omitempty"`
	At    time.Time         `json:"at"`
}

// LoadLast reads the last session from dir. It reports false if there is
// none, or it can't be read.
func LoadLast(dir string) (LastSession, bool) {
	var l LastSession
	if dir == "" {
		return l, false
	}
	b, err := os.ReadFile(filepath.Join(dir, lastFile))
	if err != nil || json.Unmarshal(b, &l) != nil || len(l.Names) == 0 {
		return LastSession{}, false
	}
	return l, true
}

// Registered returns the names of the last session that are still
// registered as projects or groups, the extra arguments of registered
// projects, and the names skipped.
func (l LastSession) Registered(cfg *config.Config) (names []string, args map[string]string, skipped []string) {
	for _, n := range l.Names {
		if cfg.Projects[n] != nil || cfg.Groups[n] != nil {
			names = append(names, n)
		} else {
			skipped = append(skipped, n)
		}
	}
	for p, a := range l.Args {
		if cfg.Projects[p] != nil {
			if args == nil {
				args = map[string]string{}
			}
			args[p] = a
		}
	}
	return names, args, skipped
}

// Recorder records a session as the last session, saving it in dir after
// every change once something was started. A Recorder with dir "" records
// nothing. It is safe for concurrent use.
type Recorder struct {
	dir  string
	now  func() time.Time
	mu   sync.Mutex
	last LastSession
}

// NewRecorder returns a recorder for a new session, saving in dir.
func NewRecorder(dir string) *Recorder {
	return &Recorder{dir: dir, now: time.Now}
}

// Started records that names were started, after those started before.
func (r *Recorder) Started(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		if !slices.Contains(r.last.Names, n) {
			r.last.Names = append(r.last.Names, n)
		}
	}
	r.save()
}

// SetArgs records a project's extra arguments; blank ones remove them.
func (r *Recorder) SetArgs(project, args string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if args = strings.TrimSpace(args); args == "" {
		delete(r.last.Args, project)
	} else {
		if r.last.Args == nil {
			r.last.Args = map[string]string{}
		}
		r.last.Args[project] = args
	}
	r.save()
}

// save writes the session atomically, once something was started.
func (r *Recorder) save() {
	if r.dir == "" || len(r.last.Names) == 0 {
		return
	}
	r.last.At = r.now()
	b, err := json.MarshalIndent(r.last, "", "  ")
	if err != nil {
		return
	}
	_ = writeFile(filepath.Join(r.dir, lastFile), append(b, '\n'))
}

// writeFile replaces path with data through a temporary file, so a reader
// never sees half of it.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".last-session-*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}
