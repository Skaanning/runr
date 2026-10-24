//go:build windows

package proc

import (
	"os"
	"os/exec"
)

// Minimal placeholder so the package builds on Windows; T8 replaces this with
// process groups, CTRL_BREAK and Job Objects.

func newCommand(command string) *exec.Cmd {
	return exec.Command("cmd", "/C", command)
}

type platformHandle struct {
	process *os.Process
}

func attach(cmd *exec.Cmd) (platformHandle, error) {
	return platformHandle{process: cmd.Process}, nil
}

func (h platformHandle) terminate() error { return h.kill() }
func (h platformHandle) kill() error      { return h.process.Kill() }
func (h platformHandle) release()         {}

func exitSignal(*os.ProcessState) string { return "" }
