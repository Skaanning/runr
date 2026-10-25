package ports

import (
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const tcpTableOwnerPidListener = 3 // TCP_TABLE_OWNER_PID_LISTENER

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// listeners reads the IPv4 and IPv6 listener tables with
// GetExtendedTcpTable, which name each socket's owning process.
func listeners(want map[int]bool) ([]Listener, error) {
	if getExtendedTCPTable.Find() != nil {
		return nil, ErrUnsupported
	}
	var ls []Listener
	for _, af := range []uint32{windows.AF_INET, windows.AF_INET6} {
		table, err := tcpTable(af)
		if err != nil {
			return nil, err
		}
		rows, err := parseTCPTable(table, af == windows.AF_INET6, want)
		if err != nil {
			return nil, err
		}
		ls = append(ls, rows...)
	}
	return ls, nil
}

// tcpTable returns the raw listener table of an address family, growing
// the buffer while the table grows between calls.
func tcpTable(af uint32) ([]byte, error) {
	size := uint32(4096)
	for range 8 {
		buf := make([]byte, size)
		r, _, _ := getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
			0, uintptr(af), tcpTableOwnerPidListener, 0)
		switch windows.Errno(r) {
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			continue // size now holds what is needed
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, errors.New("GetExtendedTcpTable: the table keeps growing")
}

func processName(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	name := filepath.Base(windows.UTF16ToString(buf[:size]))
	if ext := filepath.Ext(name); strings.EqualFold(ext, ".exe") {
		name = name[:len(name)-len(ext)] // "node", like on the other systems
	}
	return name
}
