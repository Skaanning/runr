package tui

import "os/exec"

func detachBrowser(*exec.Cmd) {} // not tied to the console on Windows
