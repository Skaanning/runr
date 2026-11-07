//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// PTYSupported reports whether Spec.PTY runs commands on a pseudo-terminal
// here.
const PTYSupported = true

// openPTY opens a pseudo-terminal of cols × rows and connects cmd to its
// slave end as the controlling terminal of a new session. The session's
// process group is the shell's pid, as with pipes, so signalling the group
// and finding leftovers work the same.
func openPTY(cmd *exec.Cmd, cols, rows int) (master, slave *os.File, err error) {
	master, slave, err = pty.Open()
	if err != nil {
		return nil, nil, err
	}
	if err := setSize(master, cols, rows); err != nil {
		master.Close()
		slave.Close()
		return nil, nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	return master, slave, nil
}

func setSize(f *os.File, cols, rows int) error {
	c, r := termSize(cols, rows)
	return pty.Setsize(f, &pty.Winsize{Cols: c, Rows: r})
}
