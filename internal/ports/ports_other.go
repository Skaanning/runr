//go:build !linux && !darwin && !windows

package ports

func listeners(map[int]bool) ([]Listener, error) { return nil, ErrUnsupported }

func processName(int) string { return "" }
