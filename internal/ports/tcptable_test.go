package ports

import (
	"encoding/binary"
	"slices"
	"testing"
)

// tcpRow builds a MIB_TCPROW_OWNER_PID (v6: MIB_TCP6ROW_OWNER_PID) for a
// listener as GetExtendedTcpTable returns it on little-endian Windows.
func tcpRow(v6 bool, port, pid int) []byte {
	row := make([]byte, tcpRowSize)
	portAt, pidAt := 8, 20
	if v6 {
		row = make([]byte, tcp6RowSize)
		portAt, pidAt = 20, 52
		binary.LittleEndian.PutUint32(row[48:], 2) // state: MIB_TCP_STATE_LISTEN
		row[15] = 1                                // ::1
	} else {
		binary.LittleEndian.PutUint32(row, 2)
		copy(row[4:], []byte{127, 0, 0, 1})
	}
	binary.BigEndian.PutUint16(row[portAt:], uint16(port))
	row[portAt+2], row[portAt+3] = 0xAA, 0xBB // garbage in the DWORD's high half
	binary.LittleEndian.PutUint32(row[pidAt:], uint32(pid))
	return row
}

func tcpTableBytes(v6 bool, rows ...[2]int) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(rows)))
	for _, r := range rows {
		b = append(b, tcpRow(v6, r[0], r[1])...)
	}
	return append(b, 0, 0, 0, 0) // padding after the table is ignored
}

func TestParseTCPTable(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		b := tcpTableBytes(v6, [2]int{8080, 4321}, [2]int{445, 4}, [2]int{5173, 999})
		got, err := parseTCPTable(b, v6, nil)
		want := []Listener{{4321, 8080}, {4, 445}, {999, 5173}}
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("v6=%v: all = %v, %v, want %v", v6, got, err, want)
		}
		got, err = parseTCPTable(b, v6, map[int]bool{999: true, 4321: true})
		want = []Listener{{4321, 8080}, {999, 5173}}
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("v6=%v: filtered = %v, %v, want %v", v6, got, err, want)
		}
		if _, err := parseTCPTable(b[:len(b)-10], v6, nil); err == nil {
			t.Errorf("v6=%v: truncated table parsed", v6)
		}
	}
	if got, err := parseTCPTable([]byte{0, 0, 0, 0}, false, nil); err != nil || len(got) != 0 {
		t.Errorf("empty table = %v, %v", got, err)
	}
	if _, err := parseTCPTable(nil, false, nil); err == nil {
		t.Error("no table parsed")
	}
}
