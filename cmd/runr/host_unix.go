//go:build !windows

package main

import "syscall"

// hostSupported reports whether sessions run in a host (runr host) here.
const hostSupported = true

// hostAttr starts a host in a session of its own: the terminal's Ctrl+C
// and its closing (SIGHUP) don't reach it.
func hostAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// processAlive reports whether a process with pid exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
