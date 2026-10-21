package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sorokin-vladimir/tele/internal/store"
)

// clockAt makes o's clock read t until moved.
func clockAt(o *Owner, t time.Time) *time.Time {
	now := t
	o.now = func() time.Time { return now }
	return &now
}

// The owner knows when it last heard from Telegram: whoever asks the host how
// it is doing learns whether anything is arriving at all (#208).
func TestHealth_RecordsWhenTheLastUpdateArrived(t *testing.T) {
	o, events, _ := newTestOwner(t)
	start := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	now := clockAt(o, start)
	assert.True(t, o.Health().LastUpdate.IsZero(), "nothing has arrived yet")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.RunUpdates(ctx)
	*now = start.Add(time.Minute)
	events <- store.Event{Kind: store.EventChannelGap, ChatID: 1}

	assert.Eventually(t, func() bool { return o.Health().LastUpdate.Equal(start.Add(time.Minute)) },
		time.Second, 5*time.Millisecond)
}

// The owner knows whether its link to Telegram is up, and since when: a link
// down for an hour is a different answer from one that dropped a second ago.
func TestHealth_SaysWhetherTheLinkIsUpAndSinceWhen(t *testing.T) {
	o, _, _ := newTestOwner(t)
	start := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	now := clockAt(o, start)
	assert.Equal(t, LinkUnknown, o.Health().Link, "not connected yet")

	o.SetLink(false)
	assert.Equal(t, LinkUnknown, o.Health().Link, "a link that was never up has not dropped")

	o.SetLink(true)
	*now = start.Add(time.Hour)
	o.SetLink(true)

	h := o.Health()
	assert.Equal(t, LinkUp, h.Link)
	assert.Equal(t, start, h.LinkSince, "still up is not up anew")

	*now = start.Add(2 * time.Hour)
	o.SetLink(false)

	h = o.Health()
	assert.Equal(t, LinkDown, h.Link)
	assert.Equal(t, start.Add(2*time.Hour), h.LinkSince)
}
