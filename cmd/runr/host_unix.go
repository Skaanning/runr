//go:build !windows

package main

import (
	"os"
	"syscall"
)

// hostSupported reports whether sessions run in a host (runr host) here.
const hostSupported = true

// hostAttrs are the ways to start a host, tried in order: in a session of
// its own, so that the terminal's Ctrl+C and its closing (SIGHUP) don't
// reach it.
func hostAttrs() []*syscall.SysProcAttr {
	return []*syscall.SysProcAttr{{Setsid: true}}
}

// createHostLog creates a new log file in dir for a host's stderr.
func createHostLog(dir string) (*os.File, error) {
	return os.CreateTemp(dir, "starting-*.log")
}

// processAlive reports whether a process with pid exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
