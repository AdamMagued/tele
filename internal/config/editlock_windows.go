//go:build windows

package config

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile waits for an exclusive lock on the file's first byte. The lock file
// holds no content, so the range is a token and nothing is ever read under it.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{})
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
