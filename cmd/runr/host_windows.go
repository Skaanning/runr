package main

import "syscall"

// hostSupported reports whether sessions run in a host (runr host) here.
// Not on Windows yet: sessions run inside runr up and the launcher.
const hostSupported = false

func hostAttr() *syscall.SysProcAttr { return nil }

// processAlive is true: there are no hosts whose logs could be pruned.
func processAlive(int) bool { return true }
