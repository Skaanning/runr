//go:build windows

package proc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conPTY is a pseudo console and the pipes runr talks to it through.
type conPTY struct {
	mu  sync.Mutex
	hpc windows.Handle // 0 once closed
	in  *os.File       // input to the console
	out *os.File       // the console's output
}

// startOnTerminal starts cmd on a new pseudo console of cols × rows. os/exec
// can't attach a process to one, so it is created here, the way os/exec
// would, with the console in its attribute list. It sets cmd.Process and
// returns the console and the function that waits for cmd.
//
// The process gets no CREATE_NEW_PROCESS_GROUP: it has a console of its own,
// and that flag would turn off the Ctrl+C that terminate types into it.
func startOnTerminal(cmd *exec.Cmd, cols, rows int) (terminal, func() error, error) {
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CmdLine == "" {
		return nil, nil, errors.New("no command line") // newCommand sets one
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, nil, err
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return nil, nil, err
	}
	t := &conPTY{in: os.NewFile(uintptr(inWrite), "conpty-in"), out: os.NewFile(uintptr(outRead), "conpty-out")}
	err := windows.CreatePseudoConsole(consoleSize(cols, rows), inRead, outWrite, 0, &t.hpc)
	// The console has its own copies of its ends.
	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)
	if err != nil {
		t.in.Close()
		t.out.Close()
		return nil, nil, fmt.Errorf("creating a pseudo console: %w", err)
	}
	if err := t.spawn(cmd); err != nil {
		t.exited()
		t.close()
		return nil, nil, err
	}
	wait := func() error {
		state, err := cmd.Process.Wait()
		cmd.ProcessState = state
		return err
	}
	return t, wait, nil
}

// spawn creates cmd's process attached to the console.
func (t *conPTY) spawn(cmd *exec.Cmd) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&t.hpc)), unsafe.Sizeof(t.hpc)); err != nil {
		return err
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	// Standard handles of none: the process uses the console's, not runr's.
	si.Flags = windows.STARTF_USESTDHANDLES

	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return err
	}
	line, err := windows.UTF16PtrFromString(cmd.SysProcAttr.CmdLine)
	if err != nil {
		return err
	}
	var dir *uint16
	if cmd.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return err
		}
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	block, err := envBlock(env)
	if err != nil {
		return err
	}

	var pi windows.ProcessInformation
	// Suspended until attach has put it in its Job Object, as with pipes.
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	err = withCtrlC(func() error {
		return windows.CreateProcess(app, line, nil, nil, false, flags, block, dir, &si.StartupInfo, &pi)
	})
	if err != nil {
		return err
	}
	windows.CloseHandle(pi.Thread)
	// Holding pi.Process while looking the process up keeps its pid from
	// being reused.
	defer windows.CloseHandle(pi.Process)
	p, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return err
	}
	cmd.Process = p
	return nil
}

var (
	setConsoleCtrlHandler = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	ctrlCMu               sync.Mutex // withCtrlC changes a setting of the whole process
)

// ctrlCIgnored is the ConsoleFlags bit set while the process ignores Ctrl+C.
const ctrlCIgnored = 0x1

// withCtrlC runs create (a CreateProcess) with Ctrl+C not ignored by runr.
// A process created while Ctrl+C is ignored ignores it too, and on a pseudo
// console that would make it deaf to the Ctrl+C terminate types. runr
// itself may well start that way: programs run from Git Bash do. runr's own
// setting is restored afterwards.
func withCtrlC(create func() error) error {
	ctrlCMu.Lock()
	defer ctrlCMu.Unlock()
	if windows.RtlGetCurrentPeb().ProcessParameters.ConsoleFlags&ctrlCIgnored == 0 {
		return create()
	}
	if ok, _, _ := setConsoleCtrlHandler.Call(0, 0); ok == 0 {
		return create() // it will ignore Ctrl+C; stopping falls back to the kill
	}
	defer setConsoleCtrlHandler.Call(0, 1)
	return create()
}

// envBlock returns env (KEY=VALUE pairs) as CreateProcess takes it: UTF-16
// strings, each ending in NUL, and a NUL after the last.
func envBlock(env []string) (*uint16, error) {
	var b []uint16
	for _, kv := range env {
		for _, r := range kv {
			if r == 0 {
				return nil, fmt.Errorf("environment variable with a NUL: %q", kv)
			}
		}
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	if len(b) == 0 {
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0], nil
}

// consoleSize is cols × rows as a console size.
func consoleSize(cols, rows int) windows.Coord {
	c, r := termSize(cols, rows)
	return windows.Coord{X: int16(min(c, 0x7fff)), Y: int16(min(r, 0x7fff))}
}

func (t *conPTY) Read(b []byte) (int, error)  { return t.out.Read(b) }
func (t *conPTY) Write(b []byte) (int, error) { return t.in.Write(b) }

// clean keeps the colors of the console's output and drops the rest of its
// escape sequences (see consoleLine).
func (t *conPTY) clean(line string) string { return consoleLine(line) }

func (t *conPTY) resize(cols, rows int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.hpc == 0 {
		return nil
	}
	return windows.ResizePseudoConsole(t.hpc, consoleSize(cols, rows))
}

// exited closes the console: its output pipe stays open until then, also
// after every process on it has exited. The console writes what it has left
// first (older Windows versions block until it is read, which the reader
// goroutine does).
func (t *conPTY) exited() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.hpc != 0 {
		windows.ClosePseudoConsole(t.hpc)
		t.hpc = 0
	}
}

// close closes the pipes, and the console if exited hasn't: the output pipe
// first, so that closing the console can't wait for output nobody reads.
func (t *conPTY) close() {
	t.out.Close()
	t.exited()
	t.in.Close()
}
