//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func newCommand(command string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Pass the command line verbatim: with /S, cmd strips the outer quotes
		// and runs the rest exactly as written (Go's default argument escaping
		// uses backslashes, which cmd.exe does not understand). /D skips
		// AutoRun scripts.
		CmdLine: `"` + shell + `" /d /s /c "` + command + `"`,
		// A new process group lets us send CTRL_BREAK to just this command and
		// keeps it from receiving the Ctrl+C typed into runr's console.
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd
}

// platformHandle tracks the Job Object holding the process tree. Processes
// started by the command join the job automatically, so terminating the job
// stops the whole tree.
type platformHandle struct {
	*jobState
}

type jobState struct {
	mu       sync.Mutex
	job      windows.Handle // 0 if the process could not be assigned to a job
	pid      uint32
	process  *os.Process
	released bool
}

func attach(cmd *exec.Cmd) (platformHandle, error) {
	s := &jobState{pid: uint32(cmd.Process.Pid), process: cmd.Process}
	// Failing to set up the job is not fatal (e.g. the process already
	// exited); kill then falls back to stopping just the shell process.
	if job, err := newJob(s.pid); err == nil {
		s.job = job
	}
	return platformHandle{s}, nil
}

func newJob(pid uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	// Kill everything in the job when its last handle closes, so the process
	// tree also dies if runr itself exits unexpectedly.
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}

	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// terminate sends CTRL_BREAK to the command's process group. If that is not
// possible (e.g. runr has no console), it falls back to a forced kill.
func (s *jobState) terminate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil
	}
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, s.pid); err != nil {
		return s.killLocked()
	}
	return nil
}

func (s *jobState) kill() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil
	}
	return s.killLocked()
}

func (s *jobState) killLocked() error {
	if s.job != 0 {
		return windows.TerminateJobObject(s.job, 1)
	}
	if err := s.process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// release closes the job once the command has exited. Because of
// KILL_ON_JOB_CLOSE this also stops any children it left running.
func (s *jobState) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != 0 {
		windows.CloseHandle(s.job)
		s.job = 0
	}
	s.released = true
}

func exitSignal(*os.ProcessState) string { return "" }
