package hosting

import (
	"testing"
	"time"
)

// WaitForHostFor shortens how long Reach waits for a host that holds the lock
// to serve the endpoint, for the length of the test.
func WaitForHostFor(t *testing.T, d time.Duration) {
	t.Helper()
	was := waitForHost
	waitForHost = d
	t.Cleanup(func() { waitForHost = was })
}

// AwaitPauseFor shortens how often Await looks whether the state directory is
// free, for the length of the test.
func AwaitPauseFor(t *testing.T, d time.Duration) {
	t.Helper()
	was := awaitPause
	awaitPause = d
	t.Cleanup(func() { awaitPause = was })
}
