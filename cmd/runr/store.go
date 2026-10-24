package main

import (
	"fmt"
	"io"

	"github.com/Skaanning/runr/internal/config"
)

// store is the loaded config plus the path it came from.
type store struct {
	path string
	cfg  *config.Config
}

func loadStore() (*store, error) {
	path, err := config.Path()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return &store{path: path, cfg: cfg}, nil
}

// save validates the config and writes it. Nothing is written if validation
// fails. The returned warnings are non-fatal problems in the whole config.
func (s *store) save() ([]config.Issue, error) {
	warnings, err := s.cfg.Validate()
	if err != nil {
		return nil, err
	}
	return warnings, config.Save(s.path, s.cfg)
}

// printWarnings writes the warnings that concern the given project or group
// (all warnings if name is empty).
func printWarnings(w io.Writer, warnings []config.Issue, name string) {
	for _, issue := range warnings {
		if name == "" || issue.Name == name {
			fmt.Fprintf(w, "warning: %s\n", issue)
		}
	}
}
