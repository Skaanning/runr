//go:build !windows

package tui

import (
	"os/exec"
	"syscall"
)

// detachBrowser starts the opener in its own session, so a browser it
// starts outlives runr's terminal.
func detachBrowser(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
