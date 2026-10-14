//go:build windows

package ipc

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/windows"
)

// noHost reports a dial that found nobody listening: no socket file, or one
// nothing answers on. Windows says the second with Winsock's own code, which
// the syscall package does not map to ECONNREFUSED.
func noHost(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, windows.WSAECONNREFUSED)
}
