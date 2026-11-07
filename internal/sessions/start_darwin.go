package sessions

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// procStart returns when a process started (kern.proc.pid's p_starttime),
// as "seconds.microseconds".
func procStart(pid int) (string, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(p.Proc.P_pid) != pid {
		return "", os.ErrNotExist // no such process
	}
	t := p.Proc.P_starttime
	return strconv.FormatInt(int64(t.Sec), 10) + "." + strconv.FormatInt(int64(t.Usec), 10), nil
}
