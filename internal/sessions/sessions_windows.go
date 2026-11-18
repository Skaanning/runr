package sessions

import "time"

// On Windows every process runr starts is in a Job Object that is killed
// with runr, so nothing is left over and nothing is recorded.

// Dir returns ErrUnsupported.
func Dir() (string, error) { return "", ErrUnsupported }

// Recorder does nothing on Windows.
type Recorder struct{}

// Open returns ErrUnsupported.
func Open(string) (*Recorder, error) { return nil, ErrUnsupported }

func (*Recorder) Spawned(string, int, string, string, string) {}
func (*Recorder) Exited(string)                               {}
func (*Recorder) Close()                                      {}

// Find finds nothing.
func Find(string) ([]Orphan, error) { return nil, nil }

// Kill does nothing.
func Kill([]Orphan, time.Duration) []Child { return nil }

// Forget does nothing.
func Forget([]Orphan) {}
