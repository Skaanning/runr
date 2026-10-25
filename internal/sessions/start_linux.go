package sessions

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

func stateDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

// procStart returns when a process started: field 22 of /proc/<pid>/stat,
// in clock ticks since boot.
func procStart(pid int) (string, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	return statStart(stat)
}

// statStart picks the start time out of /proc/<pid>/stat. The fields after
// the command name (which may contain spaces and parentheses) start at
// field 3, so field 22 is the 20th.
func statStart(stat []byte) (string, error) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", errors.New("malformed /proc stat")
	}
	fields := bytes.Fields(stat[end+1:])
	if len(fields) < 20 {
		return "", errors.New("malformed /proc stat")
	}
	return string(fields[19]), nil
}
