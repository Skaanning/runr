package ports

import (
	"maps"
	"strings"
	"testing"
)

func TestParseProcNet(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 41736 1 0000000000000000 100 0 0 10 0
   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1234 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F90 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 99999 1 0000000000000000 20 4 30 10 -1
   3: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 0 1 0000000000000000 100 0 0 10 0
`
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 55555 1 0000000000000000 100 0 0 10 0
   1: short line
`
	socks := map[uint64]int{}
	parseProcNet(strings.NewReader(tcp), socks)
	parseProcNet(strings.NewReader(tcp6), socks)
	// The established connection (st 01) and the socket with inode 0
	// (another network namespace's, or closing) are skipped.
	want := map[uint64]int{41736: 8080, 1234: 22, 55555: 3000}
	if !maps.Equal(socks, want) {
		t.Errorf("sockets = %v, want %v", socks, want)
	}
}

func TestSocketInode(t *testing.T) {
	for link, want := range map[string]uint64{"socket:[41736]": 41736, "pipe:[123]": 0, "socket:[x]": 0, "/dev/null": 0} {
		got, ok := socketInode(link)
		if got != want || ok != (want != 0) {
			t.Errorf("socketInode(%q) = %d, %v", link, got, ok)
		}
	}
}
