package ports

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// parseLsof reads the output of `lsof -nP -iTCP -sTCP:LISTEN -Fpn`: a
// "p<pid>" line per process, followed by "f<fd>" and "n<address>" lines for
// each of its sockets, e.g. "n*:8080", "n127.0.0.1:5173" or "n[::1]:3000".
// Processes not in want (nil: every process) are skipped.
func parseLsof(out []byte, want map[int]bool) []Listener {
	var ls []Listener
	pid := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid <= 0 || want != nil && !want[pid] {
				continue
			}
			addr := line[1:]
			if strings.Contains(addr, "->") {
				continue // a connection, not a listener
			}
			i := strings.LastIndexByte(addr, ':')
			if i < 0 {
				continue
			}
			if port, err := strconv.Atoi(addr[i+1:]); err == nil && port > 0 && port <= 65535 {
				ls = append(ls, Listener{Pid: pid, Port: port})
			}
		}
	}
	return ls
}
