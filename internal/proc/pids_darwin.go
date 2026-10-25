package proc

import "golang.org/x/sys/unix"

// treePids lists the members of each process group with the
// kern.proc.pgrp sysctl.
func treePids(hs []platformHandle) [][]int {
	out := make([][]int, len(hs))
	for i, h := range hs {
		procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", h.pgid)
		if err != nil {
			continue
		}
		for _, p := range procs {
			out[i] = append(out[i], int(p.Proc.P_pid))
		}
	}
	return out
}
