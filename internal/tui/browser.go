package tui

import (
	"os/exec"
	"runtime"
	"time"
)

// browserWait is how long openURL waits for the opener to fail; one still
// running then (it may stay until the browser it started exits) counts as
// success.
const browserWait = 2 * time.Second

// openURL opens url in the default browser: xdg-open on Linux and other
// Unix systems, open on macOS, url.dll on Windows. The opener runs detached
// from runr's terminal, with no input or output.
var openURL = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	detachBrowser(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(browserWait):
		return nil
	}
}
