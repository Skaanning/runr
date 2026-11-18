// Package importer reads the programs defined in a Procfile or a Docker
// Compose file, for runr import.
package importer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Format is the kind of file being imported.
type Format string

const (
	Procfile Format = "procfile"
	Compose  Format = "compose"
)

// Formats lists the supported formats.
var Formats = []Format{Procfile, Compose}

// Candidates are the file names looked for in a directory, in order.
var Candidates = []string{"Procfile", "compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// Service is one program found in a file.
type Service struct {
	Name string // as written in the file
	// Cmd is a shell command line (Procfile); Args the program and its
	// arguments, which the caller quotes for the shell (compose).
	Cmd  string
	Args []string
	// StopArgs stops the service's container (compose), for stop_cmd: the
	// container outlives a killed `docker compose up`.
	StopArgs []string
	// DependsOn names other services of the same file.
	DependsOn []string
	// Port is the first TCP port published on the host, 0 if none or if
	// the service is a one-shot task.
	Port int
	// OneShot is set for a compose service that another one waits for with
	// condition: service_completed_successfully.
	OneShot bool
}

// File is the result of reading a file.
type File struct {
	Format   Format
	Path     string // absolute
	Dir      string // the file's directory, where the services run
	Name     string // the compose project name (top-level name:), if any
	Services []Service
	// EnvFile is ".env" when a Procfile has one next to it (foreman and
	// friends load it), "" otherwise.
	EnvFile string
}

// FormatOf tells the format from a file name: Procfile* is a Procfile,
// *compose*.yaml or *compose*.yml a compose file.
func FormatOf(path string) (Format, bool) {
	base := strings.ToLower(filepath.Base(path))
	ext := filepath.Ext(base)
	switch {
	case strings.HasPrefix(base, "procfile"):
		return Procfile, true
	case strings.Contains(base, "compose") && (ext == ".yaml" || ext == ".yml"):
		return Compose, true
	}
	return "", false
}

// Find returns the Candidates that exist in dir, in order.
func Find(dir string) []string {
	var found []string
	for _, name := range Candidates {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
			found = append(found, name)
		}
	}
	return found
}

// Load reads the file at path in the given format.
func Load(path string, format Format) (*File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	f := &File{Format: format, Path: abs, Dir: filepath.Dir(abs)}
	switch format {
	case Procfile:
		f.Services, err = parseProcfile(string(data))
		if _, statErr := os.Stat(filepath.Join(f.Dir, ".env")); statErr == nil {
			f.EnvFile = ".env"
		}
	case Compose:
		f.Name, f.Services, err = parseCompose(data, composeFileArg(abs))
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	if len(f.Services) == 0 {
		return nil, fmt.Errorf("%s: nothing to import", filepath.Base(abs))
	}
	return f, nil
}

// composeFileArg is the file name to pass to docker compose -f, or "" when
// docker compose picks the file by itself in its directory (it is the first
// of the default names there).
func composeFileArg(path string) string {
	base := filepath.Base(path)
	for _, name := range Candidates[1:] {
		_, err := os.Stat(filepath.Join(filepath.Dir(path), name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if name == base {
			return ""
		}
		break
	}
	return base
}
