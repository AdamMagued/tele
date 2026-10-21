package hosting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/statedir"
)

// awaited is what Await came to, from the goroutine it ran on.
type awaited struct {
	r   hosting.Reached
	err error
}

// await runs Await on dir, saying on held each time it is told who holds it.
func await(ctx context.Context, dir string, held chan<- error) <-chan awaited {
	done := make(chan awaited, 1)
	go func() {
		r, err := hosting.Await(ctx, app, dir, func(holder error) { held <- holder })
		done <- awaited{r, err}
	}()
	return done
}

// A service never gives up on its state directory: while another tele holds
// it, the service says who and waits, and hosts once it is free (#208).
func TestAwait_HostsOnceTheHolderLetsGo(t *testing.T) {
	hosting.AwaitPauseFor(t, 10*time.Millisecond)
	dir := stateDir(t)
	holder := reach(t, dir)
	require.NotNil(t, holder.Listener)

	held := make(chan error, 100)
	done := await(context.Background(), dir, held)

	select {
	case holderErr := <-held:
		assert.ErrorIs(t, holderErr, statedir.ErrLocked)
	case <-time.After(2 * time.Second):
		t.Fatal("the service did not say the state directory is held")
	}
	select {
	case a := <-done:
		a.r.Close()
		t.Fatalf("the service went on while the state directory was held: %v", a.err)
	case <-time.After(100 * time.Millisecond):
	}
	assert.Empty(t, held, "who holds it is said once, not on every look")

	holder.Close()

	select {
	case a := <-done:
		require.NoError(t, a.err)
		t.Cleanup(a.r.Close)
		assert.NotNil(t, a.r.Listener, "the service hosts")
	case <-time.After(2 * time.Second):
		t.Fatal("the service did not take the state directory once it was free")
	}
}

// With nobody holding the state directory, a service hosts at once and says
// nothing about waiting.
func TestAwait_HostsAFreeStateDirAtOnce(t *testing.T) {
	dir := stateDir(t)
	held := make(chan error, 1)

	select {
	case a := <-await(context.Background(), dir, held):
		require.NoError(t, a.err)
		t.Cleanup(a.r.Close)
		assert.NotNil(t, a.r.Listener)
	case <-time.After(2 * time.Second):
		t.Fatal("the service did not host a free state directory")
	}
	assert.Empty(t, held)
}

// A service told to stop while it waits stops waiting.
func TestAwait_StopsWaitingWhenTold(t *testing.T) {
	hosting.AwaitPauseFor(t, 10*time.Millisecond)
	dir := stateDir(t)
	reach(t, dir)
	ctx, cancel := context.WithCancel(context.Background())

	done := await(ctx, dir, make(chan error, 100))
	cancel()

	select {
	case a := <-done:
		assert.True(t, errors.Is(a.err, context.Canceled), "got %v", a.err)
	case <-time.After(2 * time.Second):
		t.Fatal("the service kept waiting after it was told to stop")
	}
}
