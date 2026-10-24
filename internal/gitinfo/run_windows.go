//go:build windows

package gitinfo

import "os/exec"

// detach is a no-op on Windows: there is no controlling terminal to detach
// from, and on timeout only git itself is killed.
func detach(*exec.Cmd) {}
