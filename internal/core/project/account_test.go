package project_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
)

// The account is told whole on subscribing, even when nothing has happened
// yet: a fresh start is something to draw too.
func TestRegistry_AnAccountSubscriptionIsToldAtOnce(t *testing.T) {
	g, rec := newRegistry(&fakeReader{})

	subscribe(g, project.AccountWindow{})

	got := rec.take()
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Account)
	assert.Equal(t, project.AccountConnecting, got[0].Account.Contents.Phase)
}

// An account that did not change says nothing, and one that did says all of
// itself.
func TestRegistry_AnAccountSaysWhatChangedWhole(t *testing.T) {
	r := &fakeReader{}
	g, rec := newRegistry(r)
	subscribe(g, project.AccountWindow{})
	rec.take()

	g.Refresh()
	assert.Empty(t, rec.take(), "nothing changed")

	r.account = project.AccountContents{Phase: project.AccountLoggingIn, ClockSkew: time.Minute,
		Login: project.LoginStep{Kind: project.LoginCode, Seq: 1}}
	g.Refresh()
	got := rec.take()
	require.Len(t, got, 1)
	assert.Equal(t, r.account, got[0].Account.Contents)
}

// Errors are told apart by what they say: two errors with the same text are
// the same end to whoever reads it, and a new value of it is not news.
func TestRegistry_AnAccountsEndIsComparedByWhatItSays(t *testing.T) {
	r := &fakeReader{account: project.AccountContents{Ended: errors.New("refused")}}
	g, rec := newRegistry(r)
	subscribe(g, project.AccountWindow{})
	rec.take()

	r.account = project.AccountContents{Ended: errors.New("refused")}
	g.Refresh()

	assert.Empty(t, rec.take())
}

// An account the client did not take is told again whole on the next rebuild:
// it has no smaller state to be repaired from.
func TestRegistry_ALostAccountIsToldAgain(t *testing.T) {
	r := &fakeReader{}
	take := false
	var got []project.Delta
	g := project.NewRegistry(r, func(d project.Delta) bool {
		if !take {
			return false
		}
		got = append(got, d)
		return true
	})
	subscribe(g, project.AccountWindow{})

	take = true
	g.Refresh()

	require.Len(t, got, 1)
	require.NotNil(t, got[0].Account)
}

// The folders travel with the chat list: a client subscribing after they were
// loaded has them, and a change to them is a folder delta like a count's.
func TestChatList_TheFoldersComeWithTheList(t *testing.T) {
	work := domain.FolderFilter{ID: 2, Title: "Work"}
	r := &fakeReader{chats: chats(3), filters: []domain.FolderFilter{work}}

	got := project.BuildChatList(r, project.ChatListWindow{Limit: 10})
	assert.Equal(t, []domain.FolderFilter{work}, got.Folders.Filters)

	renamed := work
	renamed.Title = "Office"
	r.filters = []domain.FolderFilter{renamed}
	next := project.BuildChatList(r, project.ChatListWindow{Limit: 10})
	ds := project.DiffChatList(got, next)
	require.Len(t, ds, 1)
	assert.Equal(t, project.ChatListFolders, ds[0].Kind)
	assert.Equal(t, "Office", ds[0].Folders.Filters[0].Title)

	assert.Empty(t, project.DiffChatList(next, project.BuildChatList(r, project.ChatListWindow{Limit: 10})),
		"the same folders say nothing")
}
