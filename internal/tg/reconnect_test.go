package tg

import (
	"sync/atomic"
	"testing"

	"github.com/gotd/td/telegram"
	"github.com/stretchr/testify/require"
)

// The first Ready is the initial connect: updates.Manager runs its own startup
// getDifference, so we must NOT force a resync there. Every subsequent Ready is
// a reconnect (network change, OS sleep/wake) after which the manager's frozen
// idle/gap timers leave the state stale (#173) — those must force a catch-up.
func TestReconnectResync_ForcesOnlyAfterFirstReady(t *testing.T) {
	var calls int32
	r := &reconnectResync{force: func() { atomic.AddInt32(&calls, 1) }}

	// Initial connect.
	r.onConnectionState(telegram.ConnectionStateConnecting)
	r.onConnectionState(telegram.ConnectionStateReady)
	require.Equal(t, int32(0), atomic.LoadInt32(&calls), "must not resync on the initial connect")

	// First reconnect.
	r.onConnectionState(telegram.ConnectionStateDisconnected)
	r.onConnectionState(telegram.ConnectionStateConnecting)
	r.onConnectionState(telegram.ConnectionStateReady)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "must resync on the first reconnect")

	// Second reconnect.
	r.onConnectionState(telegram.ConnectionStateDisconnected)
	r.onConnectionState(telegram.ConnectionStateReady)
	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "must resync on every subsequent reconnect")
}

// Whether the link is up is told on every change: up once it is ready, down
// while it drops or connects again (#208).
func TestReconnectResync_ReportsTheLink(t *testing.T) {
	var seen []bool
	r := &reconnectResync{force: func() {}, link: func(up bool) { seen = append(seen, up) }}

	r.onConnectionState(telegram.ConnectionStateConnecting)
	r.onConnectionState(telegram.ConnectionStateReady)
	r.onConnectionState(telegram.ConnectionStateDisconnected)
	r.onConnectionState(telegram.ConnectionStateReady)

	require.Equal(t, []bool{false, true, false, true}, seen)
}

// With nobody to tell, the link is not told.
func TestReconnectResync_TellsNobodyWhenNobodyListens(t *testing.T) {
	r := &reconnectResync{force: func() {}}

	r.onConnectionState(telegram.ConnectionStateReady)
}

// Connecting/Disconnected transitions are lifecycle noise and must never force a
// resync on their own — only a completed (Ready) reconnect should.
func TestReconnectResync_IgnoresNonReadyStates(t *testing.T) {
	var calls int32
	r := &reconnectResync{force: func() { atomic.AddInt32(&calls, 1) }}

	r.onConnectionState(telegram.ConnectionStateConnecting)
	r.onConnectionState(telegram.ConnectionStateDisconnected)
	r.onConnectionState(telegram.ConnectionStateConnecting)
	require.Equal(t, int32(0), atomic.LoadInt32(&calls))
}
