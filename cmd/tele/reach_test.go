package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/version"
)

// A tele running another version this one cannot speak to is named, with its
// version, and the way out is said: quit it first (#208).
func TestReachFailure_AHostOnAnotherProtocolIsNamed(t *testing.T) {
	err := &dial.Mismatch{Verdict: wire.Refuse, Host: wire.Hello{Protocol: 9, Binary: "2.0.0-beta.9"}}

	msg := reachFailure(fmt.Errorf("attach: %w", err), "/home/ada/.local/state/tele")

	for _, want := range []string{"2.0.0-beta.9", version.Version, "/home/ada/.local/state/tele", "Quit it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q does not say %q", msg, want)
		}
	}
}

// A process that holds the state directory and serves nothing is named by its
// pid, with what it probably is (#208).
func TestReachFailure_AHolderThatDoesNotServeIsNamed(t *testing.T) {
	err := fmt.Errorf("%w (state directory is locked by another process (pid 4242))", hosting.ErrHeldNotServing)

	msg := reachFailure(err, "/home/ada/.local/state/tele")

	for _, want := range []string{"pid 4242", "older", "/home/ada/.local/state/tele", "Quit it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q does not say %q", msg, want)
		}
	}
}

// Anything else is said as it is.
func TestReachFailure_AnythingElseIsSaidAsItIs(t *testing.T) {
	msg := reachFailure(errors.New("permission denied"), "/x")

	if !strings.Contains(msg, "permission denied") {
		t.Errorf("%q lost the cause", msg)
	}
}
