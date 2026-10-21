package tg

import (
	"sync/atomic"

	"github.com/gotd/td/telegram"
)

// reconnectResync forces an updates.Manager catch-up (getDifference) on every
// primary-connection (re)establishment except the very first one.
//
// Why (#173): updates.Manager only re-syncs on startup, on a pts/qts/seq gap
// detected from an incoming update, or on its 15-minute idle timer. That idle
// timer is a time.Timer on the monotonic clock, which does not advance while
// macOS is suspended, so after sleep/wake it stays frozen and no getDifference
// runs until up to 15 minutes later. Meanwhile the transport reconnects without
// notifying the manager, so the chat list (counters, ordering, notifications)
// stays stale. Forcing a catch-up on reconnect closes that window immediately.
//
// The initial connect is skipped because updates.Manager already runs its own
// startup difference when it starts; forcing there would be redundant.
type reconnectResync struct {
	// force triggers a full catch-up (getDifference). It must not block the
	// caller, since onConnectionState runs synchronously in gotd's connection
	// lifecycle.
	force func()
	// seenReady records whether the initial Ready has already been observed.
	seenReady atomic.Bool
	// link is told whether the link is up on every change, nil when nobody
	// listens. It must not block either.
	link func(up bool)
}

// onConnectionState is wired to telegram.Options.OnConnectionState. It tells
// whether the link is up, and forces a resync on every Ready after the first.
func (r *reconnectResync) onConnectionState(s telegram.ConnectionState) {
	if r.link != nil {
		r.link(s == telegram.ConnectionStateReady)
	}
	if s != telegram.ConnectionStateReady {
		return
	}
	if !r.seenReady.Swap(true) {
		// First Ready is the initial connect; the manager does its own startup
		// difference, so skip to avoid a redundant getDifference.
		return
	}
	r.force()
}
