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

	// PTY runs the command on a pseudo-terminal of Cols × Rows (0: 120 × 50)
	// instead of pipes, so it behaves as in a terminal (colors, line
	// buffering). Its output is one stream (Stdout); it gets no input. On
	// Windows it is a pseudo console (ConPTY).
	PTY        bool
	Cols, Rows int

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

// Default size of a pseudo-terminal (Spec.PTY).
const (
	DefaultCols = 120
	DefaultRows = 50
)

// Process is a running command and the process group it leads.
type Process struct {
	cmd    *exec.Cmd
	done   chan struct{}
	status ExitStatus
	handle platformHandle

	termMu sync.Mutex
	term   terminal // the pseudo-terminal while it is open; nil without one
}

// terminal is the pseudo-terminal a process runs on (Spec.PTY); see
// startOnTerminal.
type terminal interface {
	io.Reader // the output of the programs on it
	io.Writer // input to them
	resize(cols, rows int) error
	// clean returns a line of output as runr keeps it.
	clean(line string) string
	// exited is called once the command has exited and its leftovers are
	// dealt with; on Windows the output only ends after it.
	exited()
	// close releases the terminal once its output has been read.
	close()
}

// Start runs spec.Command through the system shell in its own process group.
// Every line written to stdout or stderr is passed to onLine. Calls to onLine
// are never concurrent, but may continue briefly after Done is closed if the
// process left children behind that still write output.
func Start(spec Spec, onLine func(Line)) (*Process, error) {
	cmd := newCommand(spec.Command)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env

	var term terminal
	var outs []*os.File // the pipes: stdout, stderr
	wait := cmd.Wait
	if spec.PTY {
		t, w, err := startOnTerminal(cmd, spec.Cols, spec.Rows)
		if err != nil {
			return nil, fmt.Errorf("starting on a pseudo-terminal: %w", err)
		}
		term, wait = t, w
	} else {
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
		// The child has its own copies of the write ends; ours must be
		// closed so that reads hit EOF once every process holding them has
		// exited.
		outW.Close()
		errW.Close()
		if err != nil {
			outR.Close()
			errR.Close()
			return nil, err
		}
		outs = []*os.File{outR, errR}
	}
	closeOutputs := func() {
		for _, f := range outs {
			f.Close()
		}
		if term != nil {
			term.exited()
			term.close()
		}
	}

	p := &Process{cmd: cmd, done: make(chan struct{})}
	var err error
	if p.handle, err = attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = wait()
		closeOutputs()
		return nil, err
	}

	var mu sync.Mutex
	emit := func(l Line) {
		mu.Lock()
		defer mu.Unlock()
		onLine(l)
	}
	var readers sync.WaitGroup
	for i, f := range outs {
		readers.Add(1)
		go func() {
			defer readers.Done()
			defer f.Close()
			readLines(f, Stream(i), emit)
		}()
	}
	if term != nil {
		p.term = term
		p.handle.interruptWith(term)
		readers.Add(1)
		go func() {
			defer readers.Done()
			defer p.closeTerm()
			readLines(&terminalReader{t: term}, Stdout, func(l Line) {
				// Lines of nothing but the terminal's own escape sequences
				// (Windows) are dropped.
				if text := term.clean(l.Text); text != "" || l.Text == "" {
					emit(Line{Stream: l.Stream, Text: text})
				}
			})
		}()
	}
	drained := make(chan struct{})
	go func() {
		readers.Wait()
		close(drained)
	}()

	go func() {
		err := wait()
		p.status = exitStatus(cmd.ProcessState, err)
		leftoverTimeout := spec.LeftoverTimeout
		if leftoverTimeout <= 0 {
			leftoverTimeout = drainTimeout
		}
		p.status.Leftovers = p.handle.stopLeftovers(leftoverTimeout)
		if term != nil {
			term.exited()
		}
		select {
		case <-drained:
		case <-time.After(drainTimeout):
		}
		p.handle.release()
		close(p.done)
	}()
	return p, nil
}

// Resize sets the size of the process's pseudo-terminal, which tells the
// programs on it (SIGWINCH). It does nothing without one.
func (p *Process) Resize(cols, rows int) error {
	p.termMu.Lock()
	defer p.termMu.Unlock()
	if p.term == nil {
		return nil
	}
	return p.term.resize(cols, rows)
}

// closeTerm closes the pseudo-terminal once its output has been read.
func (p *Process) closeTerm() {
	p.termMu.Lock()
	defer p.termMu.Unlock()
	if p.term != nil {
		p.term.close()
		p.term = nil
	}
}

// terminalReader reads a pseudo-terminal's output and answers the programs'
// cursor position requests (ESC [ 6 n), which some send to find out about
// the terminal and wait for, with a fixed position.
type terminalReader struct {
	t terminal
}

var (
	cursorRequest = []byte("\x1b[6n")
	cursorReply   = []byte("\x1b[1;1R")
)

func (r *terminalReader) Read(b []byte) (int, error) {
	n, err := r.t.Read(b)
	if n > 0 && bytes.Contains(b[:n], cursorRequest) {
		_, _ = r.t.Write(cursorReply)
	}
	return n, err
}

// termSize returns cols × rows with defaults for zero and limits that fit a
// terminal's size fields.
func termSize(cols, rows int) (uint16, uint16) {
	if cols <= 0 {
		cols = DefaultCols
	}
	if rows <= 0 {
		rows = DefaultRows
	}
	return uint16(min(cols, 0xffff)), uint16(min(rows, 0xffff))
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
