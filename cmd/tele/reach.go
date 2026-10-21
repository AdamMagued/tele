package main

import (
	"errors"
	"fmt"

	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/version"
)

// reachFailure is what is printed when this tele can neither attach to the
// host of stateDir nor become it. It is printed before the TUI starts, so it
// says what is in the way and what to do about it (#208).
func reachFailure(err error, stateDir string) string {
	var mismatch *dial.Mismatch
	switch {
	case errors.As(err, &mismatch) && mismatch.Verdict == wire.RestartClient:
		return fmt.Sprintf("tele was updated: the service runs %s, and this is %s.\nStart tele again.",
			mismatch.Host.Binary, version.Version)
	case errors.Is(err, hosting.ErrServiceStillOlder):
		return fmt.Sprintf("The service was asked to restart on %s, but %v.\nIts unit may name an old binary; `tele daemon status` says which.",
			version.Version, err)
	case errors.Is(err, hosting.ErrNoHostCameBack):
		return "The service was asked to restart on this version and did not come back.\n`tele daemon status` says whether it runs."
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
