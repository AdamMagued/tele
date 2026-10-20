//go:build !windows

package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFile waits for an exclusive flock. The lock lives on the open file
// description, so a second descriptor for the same file waits even within one
// process.
func lockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX)
		if err != unix.EINTR {
			return err
		}
	}
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
