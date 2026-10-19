package main

import (
	"errors"
	"fmt"

	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/version"
)

// reachFailure is what is printed when this tele can neither attach to the
// host of stateDir nor become it. It is printed before the TUI starts, so it
// says what is in the way and what to do about it (#208).
func reachFailure(err error, stateDir string) string {
	var mismatch *dial.Mismatch
	switch {
	case errors.As(err, &mismatch):
		return fmt.Sprintf("tele %s is already running on %s, and this tele (%s) cannot join it.\nQuit it and start again.",
			mismatch.Host.Binary, stateDir, version.Version)
	case errors.Is(err, hosting.ErrHeldNotServing):
		return fmt.Sprintf("Another tele holds %s and does not accept connections: an older version, or one still starting (%v).\nQuit it and start again.",
			stateDir, err)
	default:
		return fmt.Sprintf("reaching the tele that holds %s: %v", stateDir, err)
	}
}
