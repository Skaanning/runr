package ports

import (
	"encoding/binary"
	"errors"
)

// Row sizes of MIB_TCPROW_OWNER_PID (state, local address, local port,
// remote address, remote port, owning pid: six DWORDs) and
// MIB_TCP6ROW_OWNER_PID (local address[16], scope id, local port, remote
// address[16], scope id, remote port, state, owning pid).
const (
	tcpRowSize  = 24
	tcp6RowSize = 56
)

var errShortTable = errors.New("tcp table is truncated")

// parseTCPTable reads a MIB_TCPTABLE_OWNER_PID (v6: MIB_TCP6TABLE_OWNER_PID)
// as returned by GetExtendedTcpTable: a DWORD entry count followed by the
// rows, in the machine's byte order (little endian on every Windows
// platform), except for the ports, which are in network byte order in the
// low 16 bits of their DWORD. Processes not in want (nil: every process)
// are skipped.
func parseTCPTable(b []byte, v6 bool, want map[int]bool) ([]Listener, error) {
	if len(b) < 4 {
		return nil, errShortTable
	}
	n := int(binary.LittleEndian.Uint32(b))
	size, portAt, pidAt := tcpRowSize, 8, 20
	if v6 {
		size, portAt, pidAt = tcp6RowSize, 20, 52
	}
	if (len(b)-4)/size < n {
		return nil, errShortTable
	}
	var ls []Listener
	for i := range n {
		row := b[4+i*size : 4+(i+1)*size]
		pid := int(binary.LittleEndian.Uint32(row[pidAt:]))
		port := int(binary.BigEndian.Uint16(row[portAt:]))
		if port == 0 || want != nil && !want[pid] {
			continue
		}
		ls = append(ls, Listener{Pid: pid, Port: port})
	}
	return ls, nil
}
