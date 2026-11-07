//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// unixTerminal is a pseudo-terminal's master end.
type unixTerminal struct {
	master *os.File
}

// startOnTerminal starts cmd on a new pseudo-terminal of cols × rows, as the
// leader of a new session with the terminal as its controlling terminal. The
// session's process group is the shell's pid, as with pipes, so signalling
// the group and finding leftovers work the same. It returns the terminal and
// the function that waits for cmd.
func startOnTerminal(cmd *exec.Cmd, cols, rows int) (terminal, func() error, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, nil, err
	}
	t := unixTerminal{master}
	if err := t.resize(cols, rows); err != nil {
		master.Close()
		slave.Close()
		return nil, nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	err = cmd.Start()
	// The child has its own copy; reads of the master end with EIO once
	// every process holding the slave end has exited.
	slave.Close()
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return t, cmd.Wait, nil
}

func (t unixTerminal) Read(b []byte) (int, error)  { return t.master.Read(b) }
func (t unixTerminal) Write(b []byte) (int, error) { return t.master.Write(b) }
func (t unixTerminal) clean(line string) string    { return line }
func (t unixTerminal) exited()                     {}
func (t unixTerminal) close()                      { t.master.Close() }

func (t unixTerminal) resize(cols, rows int) error {
	c, r := termSize(cols, rows)
	return pty.Setsize(t.master, &pty.Winsize{Cols: c, Rows: r})
}
