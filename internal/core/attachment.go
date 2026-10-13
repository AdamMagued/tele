package core

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
)

// streamSize bounds what one client may fall behind by before it starts losing
// things. Deltas and events share it: one stream keeps them in the order they
// happened.
const streamSize = 512

// Attachment is one client's handle on the owner. It carries the whole owner
// command surface plus what is the client's own: its subscriptions, the one
// stream everything addressed to it arrives on, the focus it reports and the
// detach that ends it. A client cannot report focus or subscribe on another
// client's behalf, because it never holds another client's handle (#309).
type Attachment struct {
	*Owner
	id clientID

	// registry holds this client's subscriptions, under the ids it chose.
	registry *project.Registry
	// nextSub is the last subscription id handed out. Ids only grow, so a
	// delta of a subscription just replaced is never read as the new one's.
	nextSub atomic.Uint64
	// stream carries deltas and events to the client, in the order they were
	// produced.
	stream chan any
	// resyncArmed is set while a rebuild is scheduled to repair a delta this
	// client lost, so a burst of drops schedules one rebuild rather than one
	// each (#276).
	resyncArmed atomic.Bool
}

// Attach registers a client. The caller must Detach when it goes away, or the
// owner goes on believing an abandoned chat is still on screen.
func (o *Owner) Attach() *Attachment {
	a := &Attachment{Owner: o, id: o.focus.attach(), stream: make(chan any, streamSize)}
	a.registry = project.NewRegistry(o.reader(), a.emit)
	o.clientsMu.Lock()
	o.clients[a.id] = a
	o.clientsMu.Unlock()
	return a
}

// Detach ends the attachment: its subscriptions stop costing anything and the
// focus it held is dropped.
func (a *Attachment) Detach() {
	a.clientsMu.Lock()
	delete(a.clients, a.id)
	a.clientsMu.Unlock()
	a.focus.detach(a.id)
}

// Stream is everything addressed to this client, deltas and events alike, in
// the order they were produced: project.Delta, Incoming, Typing, Progress,
// DownloadProgress, Failure and Notification.
func (a *Attachment) Stream() <-chan any { return a.stream }

// SetFocus reports the history this client is showing - a chat, or one topic
// of a forum - and the zero key for none.
func (a *Attachment) SetFocus(h domain.HistoryKey) { a.focus.set(a.id, h) }

// Subscribe registers a window under the next id of this client's and returns
// it. It does not wait for the owner: the subscription's first delta is its
// answer. Over a socket the client side chooses the id the same way.
func (a *Attachment) Subscribe(w project.Window) project.SubID {
	id := project.SubID(a.nextSub.Add(1))
	// Bring the chat's persisted tail into memory before the window is built, so
	// it paints cached history at once instead of waiting on the network — and
	// still shows something when there is no network at all (#139).
	if cw, ok := w.(project.HistoryWindow); ok {
		a.state.Store().LoadMessages(cw.History())
	}
	a.registry.Subscribe(id, w)
	a.maybeFetch(a, id, w)
	return id
}

// MoveWindow repositions a subscription. It returns immediately: over a socket a
// window move cannot be synchronous, so it is not synchronous here either.
func (a *Attachment) MoveWindow(id project.SubID, w project.Window) {
	a.registry.MoveWindow(id, w)
	a.maybeFetch(a, id, w)
}

func (a *Attachment) Unsubscribe(id project.SubID) { a.registry.Unsubscribe(id) }

// SaveMedia streams the named media into destDir, bypassing the cache, and
// returns the path it actually wrote. The owner picks the name: it follows from
// the document's own name or its MIME type, which is domain knowledge rather
// than rendering.
//
// A non-empty ref asks for progress: it comes back on this client's stream,
// under that ref, each time the whole percentage changes, and to nobody else -
// the course of a call is the caller's to watch. Media of no known size reports
// nothing, since there is no percentage to give (#204).
func (a *Attachment) SaveMedia(ctx context.Context, chatID int64, msgID int, slot domain.MediaSlot, destDir, ref string) (string, error) {
	return a.media.Save(ctx, chatID, msgID, slot, destDir, ref, func(p DownloadProgress) { a.offer(p) })
}

// offer hands one thing to the client without waiting: a client that stopped
// draining must never stall the owner. It reports whether it was taken.
func (a *Attachment) offer(v any) bool {
	select {
	case a.stream <- v:
		return true
	default:
		return false
	}
}

// resyncDelay is how long after a dropped delta the owner rebuilds on its own.
// Long enough for a stalled client to drain, short enough that the repair is
// not noticed.
const resyncDelay = 200 * time.Millisecond

// emit drops a delta rather than blocking when the client is not draining.
// Reporting the drop is what repairs it: the registry forgets what that
// subscription was told, so its next delta is a resync rather than one stated
// against a delta never received.
//
// That next delta must not wait for an unrelated change, so a drop also
// schedules one rebuild of this client's subscriptions. It runs on a timer of
// its own: emit is called under the registry's lock and must not call back
// into it.
func (a *Attachment) emit(d project.Delta) bool {
	if a.offer(d) {
		return true
	}
	a.log.Warn("projection delta dropped: client is not draining")
	if a.resyncArmed.CompareAndSwap(false, true) {
		time.AfterFunc(resyncDelay, func() {
			// Disarmed before the rebuild, so a drop during it schedules
			// another rather than being lost.
			a.resyncArmed.Store(false)
			a.registry.Refresh()
		})
	}
	return false
}
