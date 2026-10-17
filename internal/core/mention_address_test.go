package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
)

// mentionOf names user over the first five characters of the text, by id
// alone, the way a client sends it: a client holds no addresses (#208).
func mentionOf(user int64) []domain.MessageEntity {
	return []domain.MessageEntity{{Type: "mention_name", Offset: 0, Length: 5, UserID: user}}
}

func (s *stubClient) lastSentEntities() []domain.MessageEntity {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.sentEntities
}

// A mention reaches Telegram with the address the owner holds for the person,
// since the client that wrote it has none to give.
func TestMention_TheOwnerAddressesTheMentionedUser(t *testing.T) {
	c := &stubClient{}
	o, st := newCmdOwner(t, c)
	st.SetChat(domain.Chat{ID: 2, Title: "Bob", Peer: domain.Peer{ID: 2, Type: domain.PeerUser, AccessHash: 99}})
	o.SetOutbox(newOutboxStore(t))
	ctx := runWorker(t, o)

	require.NoError(t, o.Send(ctx, SendRequest{Ref: "r1", ChatID: 1, Text: "Bob, hi", Entities: mentionOf(2)}))

	waitFor(t, "the entry was never sent", func() bool { return c.sendCalls() == 1 })
	require.Len(t, c.lastSentEntities(), 1)
	assert.Equal(t, int64(99), c.lastSentEntities()[0].AccessHash)
}

// An address a client puts on a mention is not used: what reaches Telegram is
// the owner's, whoever wrote the message.
func TestMention_AnAddressFromTheClientIsIgnored(t *testing.T) {
	c := &stubClient{}
	o, st := newCmdOwner(t, c)
	st.SetChat(domain.Chat{ID: 2, Title: "Bob", Peer: domain.Peer{ID: 2, Type: domain.PeerUser, AccessHash: 99}})
	o.SetOutbox(newOutboxStore(t))
	ctx := runWorker(t, o)
	forged := mentionOf(2)
	forged[0].AccessHash = 555

	require.NoError(t, o.Send(ctx, SendRequest{Ref: "r1", ChatID: 1, Text: "Bob, hi", Entities: forged}))

	waitFor(t, "the entry was never sent", func() bool { return c.sendCalls() == 1 })
	require.Len(t, c.lastSentEntities(), 1)
	assert.Equal(t, int64(99), c.lastSentEntities()[0].AccessHash)
}

// A member offered to a client for a mention is one the owner can address
// afterwards: the members' list is where the client found the person, and the
// address it came with stays with the owner.
func TestMention_AMemberFromTheParticipantsListIsAddressed(t *testing.T) {
	c := &stubClient{}
	o, _ := newCmdOwner(t, c)
	o.SetOutbox(newOutboxStore(t))
	ctx := runWorker(t, o)
	_, err := o.GetParticipants(ctx, 1)
	require.NoError(t, err)

	require.NoError(t, o.Send(ctx, SendRequest{Ref: "r1", ChatID: 1, Text: "Ada, hi", Entities: mentionOf(7)}))

	waitFor(t, "the entry was never sent", func() bool { return c.sendCalls() == 1 })
	require.Len(t, c.lastSentEntities(), 1)
	assert.Equal(t, int64(33), c.lastSentEntities()[0].AccessHash)
}

// A caption is addressed like a text, whether it goes under one file or under
// the first part of an album.
func TestMention_ACaptionIsAddressed(t *testing.T) {
	tests := []struct {
		name  string
		files int
		got   func(*stubClient) []domain.MessageEntity
	}{
		{"one file", 1, func(c *stubClient) []domain.MessageEntity { return c.mediaEntities }},
		{"an album", 2, func(c *stubClient) []domain.MessageEntity { return c.albumEntities }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &stubClient{}
			o, st := newCmdOwner(t, c)
			st.SetChat(domain.Chat{ID: 2, Title: "Bob", Peer: domain.Peer{ID: 2, Type: domain.PeerUser, AccessHash: 99}})
			o.SetOutbox(newOutboxStore(t))
			ctx := runWorker(t, o)
			files := make([]MediaFile, tt.files)
			for i := range files {
				files[i] = MediaFile{Path: writeFile(t, fmt.Sprintf("%d.jpg", i), 4)}
			}

			require.NoError(t, o.SendMedia(ctx, MediaSendRequest{
				Ref: "r1", ChatID: 1, Files: files, Caption: "Bob, look", Entities: mentionOf(2),
			}))

			waitFor(t, "the caption was never sent", func() bool {
				c.sendMu.Lock()
				defer c.sendMu.Unlock()
				return len(tt.got(c)) == 1
			})
			c.sendMu.Lock()
			defer c.sendMu.Unlock()
			assert.Equal(t, int64(99), tt.got(c)[0].AccessHash)
		})
	}
}

// An edit is addressed like a send.
func TestMention_AnEditIsAddressed(t *testing.T) {
	c := &stubClient{}
	o, st := newCmdOwner(t, c)
	st.SetChat(domain.Chat{ID: 2, Title: "Bob", Peer: domain.Peer{ID: 2, Type: domain.PeerUser, AccessHash: 99}})
	st.SetMessages(1, []domain.Message{{ID: 5, ChatID: 1, Text: "before", Date: time.Unix(1, 0)}})

	require.NoError(t, o.EditMessage(context.Background(), 1, 5, "Bob, hi", mentionOf(2)))

	require.Len(t, c.lastSentEntities(), 1)
	assert.Equal(t, int64(99), c.lastSentEntities()[0].AccessHash)
}

// Somebody the owner holds no address for is named as plain text: the rest of
// the message, its other entities included, goes as written.
func TestMention_WithNoAddressTheNameGoesAsPlainText(t *testing.T) {
	c := &stubClient{}
	o, _ := newCmdOwner(t, c)
	o.SetOutbox(newOutboxStore(t))
	ctx := runWorker(t, o)
	entities := append(mentionOf(404), domain.MessageEntity{Type: "bold", Offset: 7, Length: 2})

	require.NoError(t, o.Send(ctx, SendRequest{Ref: "r1", ChatID: 1, Text: "Nemo, hi", Entities: entities}))

	waitFor(t, "the entry was never sent", func() bool { return c.sendCalls() == 1 })
	assert.Equal(t, []domain.MessageEntity{{Type: "bold", Offset: 7, Length: 2}}, c.lastSentEntities())
}
