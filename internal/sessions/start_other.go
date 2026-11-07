//go:build !windows && !linux && !darwin

package sessions

func procStart(int) (string, error) { return "", ErrUnsupported }
