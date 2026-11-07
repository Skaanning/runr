//go:build darwin || windows

package state

import "os"

// baseDir is the user cache directory: ~/Library/Caches on macOS,
// %LocalAppData% on Windows.
func baseDir() (string, error) { return os.UserCacheDir() }
