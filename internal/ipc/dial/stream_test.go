package dial_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ipc/ipctest"
	"github.com/sorokin-vladimir/tele/internal/ipc/protocol"
)

// A client's subscriptions reach the host under ids the client chose, in the
// order it made them, with the windows it asked for - every kind of window.
func TestSubscriptionsCrossUnderTheClientsIDs(t *testing.T) {
	rec := newRecorder()
	a := connect(t, rec)
	history := project.HistoryWindow{
		ChatID: -1001, TopicID: 3,
		Anchor: project.Anchor{Kind: project.AnchorMessage, MsgID: 42}, Before: 20, After: 20,
	}
	topics := project.TopicListWindow{ChatID: -1001, Limit: 30}

	first := a.Subscribe(project.AccountWindow{})
	second := a.Subscribe(project.ChatListWindow{Folder: 2, Limit: 40})
	a.MoveWindow(second, history)
	a.MoveWindow(second, topics)
	a.Unsubscribe(first)

	assert.Equal(t, project.SubID(1), first)
	assert.Equal(t, project.SubID(2), second)
	for _, want := range []received{
		{"SubscribeAs", []any{first, project.AccountWindow{}}},
		{"SubscribeAs", []any{second, project.ChatListWindow{Folder: 2, Limit: 40}}},
		{"MoveWindow", []any{second, history}},
		{"MoveWindow", []any{second, topics}},
		{"Unsubscribe", []any{first}},
	} {
		assert.Equal(t, want, rec.next(t))
	}
}

// Everything the host has for a client - each kind of event, and a delta -
// comes out of the client's stream as it went in, in order, less its
// addresses. The host's side is a real attachment, so what is pumped is what a
// host tells.
func TestTheStreamCrossesInOrder(t *testing.T) {
	att := core.NewAttachment()
	a := connect(t, att)

	var sent []any
	for _, ev := range protocol.Events.Members() {
		// A host that stops ends the connection on it, so nothing could
		// follow it here; it has a test of its own.
		if ev == reflect.TypeFor[core.HostStopping]() {
			continue
		}
		v := reflect.New(ev).Elem()
		ipctest.Fill(v)
		sent = append(sent, v.Interface())
	}
	delta := ipctest.Filled[project.Delta]()
	delta.History.Messages[0].Photo = &domain.PhotoRef{ID: 9, AccessHash: 7, FileReference: []byte{1}}
	sent = append(sent, delta)
	for _, v := range sent {
		att.Tell(v)
	}

	for _, v := range sent {
		select {
		case got := <-a.Stream():
			assert.Equal(t, withoutAddresses(t, v), got)
		case <-time.After(2 * time.Second):
			t.Fatalf("%T never arrived", v)
		}
	}
}

// Leaving a call - escaping a download, closing a profile - calls it off on
// the host too, and the caller does not wait for the host to notice.
func TestACallCalledOffEndsOnTheHost(t *testing.T) {
	rec := newRecorder()
	rec.block["FetchMedia"] = true
	a := connect(t, rec)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := a.FetchMedia(ctx, 1, 2, domain.PhotoFull)
		done <- err
	}()
	require.Equal(t, "FetchMedia", rec.next(t).method)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("the caller waited for the host")
	}
	ended := rec.next(t)
	assert.Equal(t, received{"FetchMedia ended", []any{context.Canceled}}, ended)
}
