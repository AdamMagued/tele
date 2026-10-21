package dial

import (
	"testing"
	"time"
)

// ReattachFor shortens how a client waits for its service, for the length of
// the test: tries at most every pauseCap, and gives up after giveUp.
func ReattachFor(t *testing.T, pauseCap, giveUp time.Duration) {
	t.Helper()
	wasCap, wasGiveUp := reattachPauseCap, giveUpAfter
	reattachPauseCap, giveUpAfter = pauseCap, giveUp
	t.Cleanup(func() { reattachPauseCap, giveUpAfter = wasCap, wasGiveUp })
}
