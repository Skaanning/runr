//go:build !windows && !linux && !darwin

package sessions

import (
	"os"
	"path/filepath"
)

func stateDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

func procStart(int) (string, error) { return "", ErrUnsupported }
