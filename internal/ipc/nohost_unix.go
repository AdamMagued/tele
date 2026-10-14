//go:build !windows

package ipc

import (
	"errors"
	"syscall"
)

// noHost reports a dial that found nobody listening: no socket file, or one
// nothing answers on.
func noHost(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}
