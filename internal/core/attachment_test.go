package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// recv reads the next T from a client's stream, passing over whatever else
// arrives first. One stream carries deltas and events alike (#309), and most
// tests are about one kind.
func recv[T any](t *testing.T, a *Attachment) (T, bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-a.Stream():
			if v, ok := ev.(T); ok {
				return v, true
			}
		case <-deadline:
			var zero T
			return zero, false
		}
	}
}

// silent reports that no T reaches the client within a short wait.
func silent[T any](t *testing.T, a *Attachment) bool {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case ev := <-a.Stream():
			if _, ok := ev.(T); ok {
				return false
			}
		case <-deadline:
			return true
		}
	}
}

// attach attaches a client and seats it at o, the way the host does.
func attach(o *Owner) *Attachment {
	a := NewAttachment()
	a.Join(o)
	return a
}

// testClients is the one client a test attaches to each owner, for the tests
// about an owner with a single client.
var (
	testClientsMu sync.Mutex
	testClients   = map[*Owner]*Attachment{}
)

// testClient is the owner's test client, attached on first use. A test that
// reads events must attach it before they happen: whoever was not attached
// when something happened never hears it.
func testClient(o *Owner) *Attachment {
	testClientsMu.Lock()
	defer testClientsMu.Unlock()
	a, ok := testClients[o]
	if !ok {
		a = attach(o)
		testClients[o] = a
	}
	return a
}

// queued takes whatever is already on a client's stream, without waiting: for a
// test whose events were produced synchronously by the call before it.
func queued(a *Attachment) []any {
	var out []any
	for {
		select {
		case ev := <-a.Stream():
			out = append(out, ev)
		default:
			return out
		}
	}
}

// only keeps the events of one kind.
func only[T any](evs []any) []T {
	var out []T
	for _, ev := range evs {
		if v, ok := ev.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func recvDelta(t *testing.T, a *Attachment) (project.Delta, bool) {
	t.Helper()
	return recv[project.Delta](t, a)
}

// Two clients on one owner each hear their own subscriptions, under the ids
// they chose. Neither reads the other's deltas off a shared channel.
func TestAttachment_EachClientHearsItsOwnSubscriptions(t *testing.T) {
	o, events, st := newTestOwner(t)
	st.SetChat(domain.Chat{ID: 1, Title: "A"})
	a, b := attach(o), attach(o)

	ida := a.Subscribe(project.ChatListWindow{Limit: 10})
	idb := b.Subscribe(project.ChatListWindow{Limit: 10})
	assert.Equal(t, ida, idb, "an id is the client's own, not the owner's")
	_, _ = recvDelta(t, a)
	_, _ = recvDelta(t, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.RunUpdates(ctx)
	events <- store.Event{Kind: store.EventMuteUpdate, ChatID: 1, Muted: true}

	for _, c := range []*Attachment{a, b} {
		d, ok := recvDelta(t, c)
		require.True(t, ok, "every client watching the list hears the change")
		require.NotNil(t, d.ChatList)
		assert.True(t, d.ChatList.Row.Muted)
	}
}

// A subscription id is never handed out twice on one attachment: a delta of a
// subscription just replaced must not be read as the new one's.
func TestAttachment_SubscriptionIDsAreNeverReused(t *testing.T) {
	o, _, _ := newTestOwner(t)
	a := attach(o)

	first := a.Subscribe(project.ChatListWindow{Limit: 10})
	a.Unsubscribe(first)
	second := a.Subscribe(project.ChatListWindow{Limit: 10})

	assert.NotEqual(t, first, second)
}

// A client that stopped draining loses its own deltas and nobody else's, and
// is resynced on its own once it drains again.
func TestAttachment_AStalledClientDoesNotStarveAnother(t *testing.T) {
	o, events, st := newTestOwner(t)
	st.SetChat(domain.Chat{ID: 1, Title: "A"})
	stalled, live := attach(o), attach(o)
	stalled.Subscribe(project.ChatListWindow{Limit: 10})
	live.Subscribe(project.ChatListWindow{Limit: 10})
	_, _ = recvDelta(t, stalled)
	_, _ = recvDelta(t, live)
	for len(stalled.stream) < cap(stalled.stream) {
		stalled.stream <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.RunUpdates(ctx)
	events <- store.Event{Kind: store.EventMuteUpdate, ChatID: 1, Muted: true}

	d, ok := recvDelta(t, live)
	require.True(t, ok, "the live client is not held back by the stalled one")
	assert.Equal(t, project.ChatListRow, d.ChatList.Kind)

	for len(stalled.stream) > 0 {
		<-stalled.stream
	}
	d, ok = recvDelta(t, stalled)
	require.True(t, ok, "a client that drains again is resynced without another change")
	assert.Equal(t, project.ChatListReset, d.ChatList.Kind)
	assert.True(t, silent[project.Delta](t, live), "the resync is the stalled client's alone")
}

// A client that detached hears nothing more, and its subscriptions cost the
// owner nothing.
func TestAttachment_ADetachedClientHearsNothing(t *testing.T) {
	o, events, st := newTestOwner(t)
	st.SetChat(domain.Chat{ID: 1})
	a := attach(o)
	a.Subscribe(project.ChatListWindow{Limit: 10})
	_, _ = recvDelta(t, a)
	a.Detach()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.RunUpdates(ctx)
	events <- store.Event{Kind: store.EventMuteUpdate, ChatID: 1, Muted: true}

	assert.True(t, silent[project.Delta](t, a))
}

// Something that happened reaches every client attached when it happened: a
// toast and a row flash are not one client's to have.
func TestAttachment_EventsReachEveryClient(t *testing.T) {
	o, st := newTestOwnerNotified(t, &mockNotifier{})
	st.SetChat(domain.Chat{ID: 2, Title: "Bob"})
	a, b := attach(o), attach(o)

	o.handleEvent(store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: 1, ChatID: 2, Text: "hello", Date: time.Now()}})

	for _, c := range []*Attachment{a, b} {
		n, ok := recv[Notification](t, c)
		require.True(t, ok, "every client draws the toast")
		assert.Equal(t, int64(2), n.ChatID)
	}
	// Read in a second pass: recv passes over what it is not looking for.
	o.handleEvent(store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: 2, ChatID: 2, Text: "again", Date: time.Now()}})
	for _, c := range []*Attachment{a, b} {
		_, ok := recv[Incoming](t, c)
		assert.True(t, ok, "every client flashes the row")
	}
}

// Download progress is the course of one call, so only the caller hears it.
func TestAttachment_DownloadProgressGoesToTheCallerOnly(t *testing.T) {
	o := sizedDocOwner(t, 1000)
	caller, other := attach(o), attach(o)

	_, err := caller.SaveMedia(context.Background(), 1, 5, domain.DocFull, t.TempDir(), "r1")
	require.NoError(t, err)

	p, ok := recv[DownloadProgress](t, caller)
	require.True(t, ok)
	assert.Equal(t, "r1", p.Ref)
	assert.True(t, silent[DownloadProgress](t, other))
}

// A window that could not be filled is the subscriber's problem to show, and
// nobody else's: another client never asked for that history.
func TestAttachment_AFailedHistoryLoadGoesToItsSubscriber(t *testing.T) {
	c := &stubConn{histErr: errors.New("boom")}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7, Type: domain.PeerUser}})
	asker, other := attach(o), attach(o)

	asker.Subscribe(project.HistoryWindow{ChatID: 7, Before: 10})

	f, ok := recv[Failure](t, asker)
	require.True(t, ok)
	assert.Equal(t, int64(7), f.ChatID)
	assert.True(t, silent[Failure](t, other))
}

// The send queue is shared, so a send it gave up on is every client's news.
func TestAttachment_AGivenUpSendReachesEveryClient(t *testing.T) {
	o, _, _ := newTestOwner(t)
	a, b := attach(o), attach(o)

	o.publishFailure(Failure{ChatID: 1, Op: OpSend, Err: errors.New("refused")})

	for _, c := range []*Attachment{a, b} {
		f, ok := recv[Failure](t, c)
		require.True(t, ok)
		assert.Equal(t, OpSend, f.Op)
	}
}

// Two clients with the same chat open: reading in one is seen in the other,
// a send started in one is in flight in the other, and the chat stays focused
// while either still shows it.
func TestAttachment_TwoClientsOnOneChat(t *testing.T) {
	o, st := newCmdOwner(t, &stubClient{})
	o.SetOutbox(newOutboxStore(t))
	st.SetChat(domain.Chat{ID: 1, Title: "Ada", Peer: domain.Peer{ID: 1, Type: domain.PeerUser}, UnreadCount: 1})
	st.SetMessages(1, []domain.Message{{ID: 9, ChatID: 1, Text: "hi", Date: time.Unix(9, 0)}})
	a, b := attach(o), attach(o)
	h := domain.HistoryKey{ChatID: 1}
	a.SetFocus(h)
	b.SetFocus(h)
	b.Subscribe(project.ChatListWindow{Limit: 10})
	// One message wide: the store holds it, so nothing goes to Telegram.
	b.Subscribe(project.HistoryWindow{ChatID: 1})
	_, _ = recvDelta(t, b)
	_, _ = recvDelta(t, b)

	require.NoError(t, a.MarkRead(context.Background(), h, 9))
	row, ok := recvRow(t, b)
	require.True(t, ok, "the other client hears the chat was read")
	assert.Zero(t, row.Unread)

	require.NoError(t, a.Send(context.Background(), SendRequest{Ref: "r1", ChatID: 1, Text: "out"}))
	entries, ok := recvOutbox(t, b)
	require.True(t, ok, "a send started in one client is in flight in the other")
	require.Len(t, entries, 1)
	assert.Equal(t, "r1", entries[0].Ref)

	a.Detach()
	assert.True(t, o.focus.focused(1), "still on screen in the other client")
	b.SetFocus(domain.HistoryKey{})
	assert.False(t, o.focus.focused(1))
}

func recvRow(t *testing.T, a *Attachment) (project.ChatRow, bool) {
	t.Helper()
	for {
		d, ok := recvDelta(t, a)
		if !ok {
			return project.ChatRow{}, false
		}
		if d.ChatList == nil {
			continue
		}
		switch d.ChatList.Kind {
		case project.ChatListRow:
			return d.ChatList.Row, true
		case project.ChatListReset:
			if len(d.ChatList.Rows) > 0 {
				return d.ChatList.Rows[0], true
			}
		}
	}
}

func recvOutbox(t *testing.T, a *Attachment) ([]domain.OutboxEntry, bool) {
	t.Helper()
	for {
		d, ok := recvDelta(t, a)
		if !ok {
			return nil, false
		}
		if d.History == nil {
			continue
		}
		switch d.History.Kind {
		case project.HistoryOutbox:
			return d.History.Outbox, true
		case project.HistoryReset:
			if len(d.History.Contents.Outbox) > 0 {
				return d.History.Contents.Outbox, true
			}
		}
	}
}
