package ports

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// listeners matches the inodes of the listening sockets in /proc/net/tcp
// and /proc/net/tcp6 against the file descriptors in /proc/<pid>/fd.
func listeners(want map[int]bool) ([]Listener, error) {
	socks := map[uint64]int{} // inode → port
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if errors.Is(err, fs.ErrNotExist) && table == "/proc/net/tcp6" {
			continue // IPv6 disabled
		}
		if err != nil {
			return nil, err
		}
		parseProcNet(f, socks)
		f.Close()
	}
	if len(socks) == 0 {
		return nil, nil
	}
	var pids []int
	if want != nil {
		for pid := range want {
			pids = append(pids, pid)
		}
	} else {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if pid, err := strconv.Atoi(e.Name()); err == nil {
				pids = append(pids, pid)
			}
		}
	}
	var ls []Listener
	for _, pid := range pids {
		dir := "/proc/" + strconv.Itoa(pid) + "/fd/"
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue // gone, or another user's process
		}
		for _, fd := range fds {
			link, err := os.Readlink(dir + fd.Name())
			if err != nil {
				continue
			}
			if inode, ok := socketInode(link); ok {
				if port, ok := socks[inode]; ok {
					ls = append(ls, Listener{Pid: pid, Port: port})
				}
			}
		}
	}
	return ls, nil
}

func processName(pid int) string {
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}
