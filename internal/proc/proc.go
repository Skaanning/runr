package proc

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Spec describes a process to start.
type Spec struct {
	Command string   // command line, run through the system shell
	Dir     string   // working directory
	Env     []string // complete environment as KEY=VALUE pairs (see BuildEnv)

	// LeftoverTimeout is how long processes the command leaves running in
	// its process group after it exits get to stop after SIGTERM before they
	// are killed. On Windows they are killed right away, unless Terminate
	// was called: then they get this long to exit after CTRL_BREAK. Zero
	// means 500ms.
	LeftoverTimeout time.Duration
}

// Stream identifies which output a line came from.
type Stream int

const (
	Stdout Stream = iota
	Stderr
)

// Line is one line of process output, without the trailing newline.
type Line struct {
	Stream Stream
	Text   string
}

// MaxLineBytes is the longest line delivered as one Line; longer lines are
// split into several.
const MaxLineBytes = 64 * 1024

// drainTimeout bounds how long Done waits for remaining output after the
// process has exited. Output can outlive the process when it leaves children
// behind that still hold its stdout/stderr open.
const drainTimeout = 500 * time.Millisecond

// leftoverPoll is how often stopLeftovers checks whether the processes the
// command left running are gone.
const leftoverPoll = 10 * time.Millisecond

// ExitStatus describes how a process ended.
type ExitStatus struct {
	Code   int    // exit code, or -1 if killed by a signal or never waited on
	Signal string // signal that killed the process (Unix), e.g. "terminated"
	Err    error  // error waiting for the process, if any

	// Leftovers reports that processes started by the command were still
	// running after it exited, and had to be stopped (on Windows: only
	// after Terminate, when they outlived the timeout).
	Leftovers bool
}

// Success reports whether the process exited with code 0.
func (s ExitStatus) Success() bool {
	return s.Code == 0 && s.Signal == "" && s.Err == nil
}

func (s ExitStatus) String() string {
	switch {
	case s.Err != nil:
		return s.Err.Error()
	case s.Signal != "":
		return "signal: " + s.Signal
	default:
		return fmt.Sprintf("exit code %d", s.Code)
	}
}

// Process is a running command and the process group it leads.
type Process struct {
	cmd    *exec.Cmd
	done   chan struct{}
	status ExitStatus
	handle platformHandle
}

// Start runs spec.Command through the system shell in its own process group.
// Every line written to stdout or stderr is passed to onLine. Calls to onLine
// are never concurrent, but may continue briefly after Done is closed if the
// process left children behind that still write output.
func Start(spec Spec, onLine func(Line)) (*Process, error) {
	cmd := newCommand(spec.Command)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env

	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdout = outW
	cmd.Stderr = errW

	err = cmd.Start()
	// The child has its own copies of the write ends; ours must be closed so
	// that reads hit EOF once every process holding them has exited.
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return nil, err
	}

	p := &Process{cmd: cmd, done: make(chan struct{})}
	if p.handle, err = attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		outR.Close()
		errR.Close()
		return nil, err
	}

	var mu sync.Mutex
	emit := func(l Line) {
		mu.Lock()
		defer mu.Unlock()
		onLine(l)
	}
	var readers sync.WaitGroup
	for _, r := range []struct {
		f      *os.File
		stream Stream
	}{{outR, Stdout}, {errR, Stderr}} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			defer r.f.Close()
			readLines(r.f, r.stream, emit)
		}()
	}
	drained := make(chan struct{})
	go func() {
		readers.Wait()
		close(drained)
	}()

	go func() {
		err := cmd.Wait()
		p.status = exitStatus(cmd.ProcessState, err)
		leftoverTimeout := spec.LeftoverTimeout
		if leftoverTimeout <= 0 {
			leftoverTimeout = drainTimeout
		}
		p.status.Leftovers = p.handle.stopLeftovers(leftoverTimeout)
		select {
		case <-drained:
		case <-time.After(drainTimeout):
		}
		p.handle.release()
		close(p.done)
	}()
	return p, nil
}

// Pid returns the process ID of the shell running the command.
func (p *Process) Pid() int {
	return p.cmd.Process.Pid
}

// Done is closed once the process and anything it left running in its process
// group have exited, and their output has been read.
func (p *Process) Done() <-chan struct{} {
	return p.done
}

// Status returns how the process ended. It is only valid after Done is closed.
func (p *Process) Status() ExitStatus {
	return p.status
}

// Terminate asks the whole process group to shut down gracefully (SIGTERM on
// Unix). It is a no-op if the group has already exited.
func (p *Process) Terminate() error {
	return p.handle.terminate()
}

// Kill forcefully stops the whole process group (SIGKILL on Unix). It is a
// no-op if the group has already exited.
func (p *Process) Kill() error {
	return p.handle.kill()
}

func exitStatus(ps *os.ProcessState, err error) ExitStatus {
	if ps == nil {
		return ExitStatus{Code: -1, Err: err}
	}
	s := ExitStatus{Code: ps.ExitCode(), Signal: exitSignal(ps)}
	if _, isExit := err.(*exec.ExitError); err != nil && !isExit {
		s.Err = err
	}
	return s
}

// readLines splits r into lines and emits them until EOF or a read error.
func readLines(r io.Reader, stream Stream, emit func(Line)) {
	br := bufio.NewReaderSize(r, MaxLineBytes)
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			emit(Line{Stream: stream, Text: cleanLine(chunk)})
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return
		}
	}
}

func cleanLine(b []byte) string {
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	return strings.ToValidUTF8(string(b), "�")
}

// Pids returns the IDs of the processes in each process's tree, in the
// order of ps: the members of its process group on Unix, of its Job Object
// on Windows. A tree that has ended, or can't be read, has none.
func Pids(ps []*Process) [][]int {
	handles := make([]platformHandle, len(ps))
	for i, p := range ps {
		handles[i] = p.handle
	}
	return treePids(handles)
}
