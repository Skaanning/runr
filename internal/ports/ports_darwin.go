package ports

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// lsofTimeout bounds one run of lsof.
const lsofTimeout = 5 * time.Second

// listeners runs lsof for every listening TCP socket and keeps those of
// the wanted processes. Without lsof, ports are not available.
func listeners(want map[int]bool) ([]Listener, error) {
	path, err := exec.LookPath("lsof")
	if err != nil {
		return nil, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn").Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(exit.Stderr) == 0 {
		err = nil // nothing is listening
	}
	if err != nil {
		return nil, err
	}
	return parseLsof(out, want), nil
}

func processName(pid int) string {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || p.Proc.P_pid != int32(pid) {
		return ""
	}
	return unix.ByteSliceToString(p.Proc.P_comm[:])
}
