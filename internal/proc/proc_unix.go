//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func newCommand(command string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", command)
	// Run in a new process group (pgid = pid) so signals reach every process
	// the command spawns, not just the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

type platformHandle struct {
	pgid int
}

func attach(cmd *exec.Cmd) (platformHandle, error) {
	return platformHandle{pgid: cmd.Process.Pid}, nil
}

func (h platformHandle) terminate() error { return h.signal(syscall.SIGTERM) }
func (h platformHandle) kill() error      { return h.signal(syscall.SIGKILL) }
func (h platformHandle) release()         {}

func (h platformHandle) signal(sig syscall.Signal) error {
	err := syscall.Kill(-h.pgid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil // group already gone
	}
	return err
}

func exitSignal(ps *os.ProcessState) string {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}
