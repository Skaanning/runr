//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"
)

func newCommand(command string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", command)
	// Run in a new process group (pgid = pid) so signals reach every process
	// the command spawns, not just the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

type platformHandle struct {
	pgid       int
	terminated *atomic.Bool // SIGTERM already sent by terminate
	killed     *atomic.Bool // SIGKILL already sent by kill
}

func attach(cmd *exec.Cmd) (platformHandle, error) {
	return platformHandle{pgid: cmd.Process.Pid, terminated: new(atomic.Bool), killed: new(atomic.Bool)}, nil
}

func (h platformHandle) terminate() error {
	h.terminated.Store(true)
	return h.signal(syscall.SIGTERM)
}

func (h platformHandle) kill() error {
	h.killed.Store(true)
	return h.signal(syscall.SIGKILL)
}
func (h platformHandle) release() {}

// stopLeftovers runs after the shell has exited and stops whatever it left
// running in its process group: SIGTERM (unless terminate already sent one),
// then SIGKILL if anything is still there after timeout. After kill there is
// no grace period: the group gets SIGKILL again and at most drainTimeout to
// go (killed processes can still be found until they are reaped, which can
// take a moment on macOS). It reports whether the group had any processes
// left.
func (h platformHandle) stopLeftovers(timeout time.Duration) bool {
	if !h.groupAlive() {
		return false
	}
	switch {
	case h.killed.Load():
		_ = h.signal(syscall.SIGKILL)
		timeout = min(timeout, drainTimeout)
	case !h.terminated.Load():
		_ = h.signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(timeout)
	for h.groupAlive() {
		if time.Now().After(deadline) {
			_ = h.signal(syscall.SIGKILL)
			break
		}
		time.Sleep(leftoverPoll)
	}
	return true
}

// groupAlive reports whether any process in the group can still be signalled.
func (h platformHandle) groupAlive() bool {
	return syscall.Kill(-h.pgid, 0) == nil
}

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
