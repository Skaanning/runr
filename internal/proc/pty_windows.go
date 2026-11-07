//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
)

// PTYSupported reports whether Spec.PTY runs commands on a pseudo-terminal
// here. Not on Windows yet (ConPTY, T52a): pipes are used.
const PTYSupported = false

func openPTY(*exec.Cmd, int, int) (master, slave *os.File, err error) {
	return nil, nil, errors.New("not supported on Windows")
}

func setSize(*os.File, int, int) error { return nil }
