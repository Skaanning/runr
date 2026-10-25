//go:build !windows && !linux && !darwin

package proc

// treePids returns just the leader of each live process group: other Unix
// systems have no portable way to list a group's members.
func treePids(hs []platformHandle) [][]int {
	out := make([][]int, len(hs))
	for i, h := range hs {
		if h.groupAlive() {
			out[i] = []int{h.pgid}
		}
	}
	return out
}
