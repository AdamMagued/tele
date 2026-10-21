package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// The host counts the clients attached to it, leaving out the one asking: a
// `tele daemon status` is not a window (#208).
func TestHostEnd_StatusCountsTheClientsButTheOneAsking(t *testing.T) {
	e, _, _ := hostEndAt(t, "")
	e.StatusFrom(func() int { return 3 }, func() *wire.AccountStatus { return nil })

	got := e.Status()

	assert.Equal(t, 2, got.Clients)
	assert.False(t, got.Since.IsZero(), "the host says when it started")
	assert.Nil(t, got.Account, "between accounts there is no account to tell of")
}

// Asked before anything is there to count, the host says what it knows.
func TestHostEnd_StatusBeforeTheHostHasClients(t *testing.T) {
	e, _, _ := hostEndAt(t, "")

	got := e.Status()

	assert.Equal(t, 0, got.Clients)
	assert.Nil(t, got.Account)
}

// The host tells how its account is doing in the protocol's words: where the
// account stands, whether its link to Telegram is up, and what is waiting to
// be sent.
func TestHost_SaysHowItsAccountIsDoing(t *testing.T) {
	h := testHostWith(t, &heldConnection{updates: make(chan store.Event)})
	acct := h.host.current()

	got := h.host.AccountStatus()
	require.NotNil(t, got)
	assert.Equal(t, wire.PhaseConnecting, got.Phase)
	assert.Equal(t, wire.LinkUnknown, got.Link)
	assert.Zero(t, got.Outbox)

	acct.owner.SetLink(true)

	got = h.host.AccountStatus()
	assert.Equal(t, wire.LinkUp, got.Link)
	assert.False(t, got.LinkSince.IsZero())
}

// A connection that ended says why.
func TestHost_SaysWhatTheConnectionEndedWith(t *testing.T) {
	h := testHostWith(t, &bannedConnection{updates: make(chan store.Event)})
	acct := h.host.current()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.host.Start(ctx)
	require.Eventually(t, func() bool { return acct.owner.Account().Ended != nil }, 2*time.Second, 5*time.Millisecond)

	got := h.host.AccountStatus()

	require.NotNil(t, got)
	assert.NotEmpty(t, got.Ended)
}
