package ports

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// tcpListen is the state column of a listening socket in /proc/net/tcp.
const tcpListen = "0A"

// parseProcNet reads a /proc/net/tcp or /proc/net/tcp6 table and returns
// the port of each listening socket by its inode:
//
//	sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
//	 0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 41736 ...
func parseProcNet(r io.Reader, socks map[uint64]int) {
	sc := bufio.NewScanner(r)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != tcpListen {
			continue
		}
		_, hexPort, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil || port == 0 {
			continue
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		socks[inode] = int(port)
	}
}

// socketInode returns the inode of a /proc/<pid>/fd link such as
// "socket:[41736]".
func socketInode(link string) (uint64, bool) {
	s, ok := strings.CutPrefix(link, "socket:[")
	if !ok {
		return 0, false
	}
	s, ok = strings.CutSuffix(s, "]")
	if !ok {
		return 0, false
	}
	inode, err := strconv.ParseUint(s, 10, 64)
	return inode, err == nil
}
