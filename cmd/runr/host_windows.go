package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// hostSupported reports whether sessions run in a host (runr host) here.
const hostSupported = true

// hostAttrs are the ways to start a host, tried in order. It gets a
// console of its own without a window (CREATE_NO_WINDOW), which its
// projects share: runr stops them with Ctrl+Break on that console, and the
// terminal's closing doesn't reach it. It leaves runr's job, if runr is in
// one (some terminals kill theirs when they close), where the job allows.
func hostAttrs() []*syscall.SysProcAttr {
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	return []*syscall.SysProcAttr{
		{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB},
		{CreationFlags: flags},
	}
}

// createHostLog creates a new log file in dir for a host's stderr. It can
// be renamed while the host has it open.
func createHostLog(dir string) (*os.File, error) {
	b := make([]byte, 8)
	rand.Read(b)
	path := filepath.Join(dir, "starting-"+hex.EncodeToString(b)+".log")
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// processAlive reports whether a process with pid is running.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED // someone else's
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == 259 // STILL_ACTIVE
}
