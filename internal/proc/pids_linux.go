package proc

import (
	"bytes"
	"os"
	"strconv"
)

// treePids finds the members of each process group with one pass over
// /proc, reading the pgid from /proc/<pid>/stat.
func treePids(hs []platformHandle) [][]int {
	out := make([][]int, len(hs))
	index := make(map[int]int, len(hs)) // pgid → position in hs
	for i, h := range hs {
		index[h.pgid] = i
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a process
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // gone already
		}
		if i, ok := index[statPgid(stat)]; ok {
			out[i] = append(out[i], pid)
		}
	}
	return out
}

// statPgid returns the process group ID from the contents of
// /proc/<pid>/stat, or -1. The fields after the command name (which may
// contain spaces and parentheses) are: state, ppid, pgrp, ...
func statPgid(stat []byte) int {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return -1
	}
	fields := bytes.Fields(stat[end+1:])
	if len(fields) < 3 {
		return -1
	}
	pgid, err := strconv.Atoi(string(fields[2]))
	if err != nil {
		return -1
	}
	return pgid
}
