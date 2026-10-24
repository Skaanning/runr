//go:build !windows

package gitinfo

import (
	"os/exec"
	"syscall"
)

// detach runs git in a new session, without a controlling terminal, so
// nothing it starts (ssh, credential helpers) can prompt on runr's terminal.
// On timeout the whole session is killed, not just git.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
