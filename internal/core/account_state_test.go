package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/core/state"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

// loginConn answers what loading a logged-in account asks: the two dialog
// lists and the folders.
type loginConn struct {
	internaltg.Client
	folders []domain.FolderFilter
}

func (loginConn) GetDialogs(context.Context) ([]domain.Chat, error) {
	return []domain.Chat{{ID: 1, Title: "Ada"}}, nil
}

func (loginConn) GetArchivedDialogs(context.Context) ([]domain.Chat, error) { return nil, nil }

func (c loginConn) GetDialogFilters(context.Context) ([]domain.FolderFilter, error) {
	return c.folders, nil
}

func (loginConn) Connect(context.Context, *internaltg.AuthFlow, chan<- struct{}, func(int64, string)) error {
	return nil
}

func (loginConn) Updates() <-chan store.Event { return nil }

// loginOwner is an owner running its login, with a client watching the
// account.
func loginOwner(t *testing.T, c Connection) (*Owner, *Attachment) {
	t.Helper()
	o := New(&config.Config{}, zap.NewNop(), state.New(store.NewMemory()), c, nopNotifier{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		o.Stop()
	})
	o.spawn(func() { o.runLogin(ctx) })
	a := o.Attach()
	a.Subscribe(project.AccountWindow{})
	first, ok := recvAccount(t, a)
	require.True(t, ok)
	require.Equal(t, project.AccountConnecting, first.Phase)
	return o, a
}

func recvAccount(t *testing.T, a *Attachment) (project.AccountContents, bool) {
	t.Helper()
	for {
		d, ok := recvDelta(t, a)
		if !ok {
			return project.AccountContents{}, false
		}
		if d.Account != nil {
			return d.Account.Contents, true
		}
	}
}

// ask puts a login step to the owner the way the connection does, and returns
// what it is answered with.
func ask(o *Owner, req internaltg.AuthRequest) <-chan internaltg.AuthResponse {
	answered := make(chan internaltg.AuthResponse, 1)
	go func() {
		o.authFlow.Requests <- req
		answered <- <-o.authFlow.Responses
	}()
	return answered
}

// A step waiting for an answer is state: every client sees it, including one
// that attaches while it waits (#309).
func TestLogin_AClientAttachingMidLoginSeesTheStep(t *testing.T) {
	o, a := loginOwner(t, &stubConn{})
	ask(o, internaltg.AuthRequest{Step: internaltg.AuthStepCode, Hint: "Enter the SMS code:"})
	_, ok := recvAccount(t, a)
	require.True(t, ok)

	late := o.Attach()
	late.Subscribe(project.AccountWindow{})

	got, ok := recvAccount(t, late)
	require.True(t, ok)
	assert.Equal(t, project.AccountLoggingIn, got.Phase)
	assert.Equal(t, project.LoginCode, got.Login.Kind)
	assert.Equal(t, "Enter the SMS code:", got.Login.Hint)
}

// One answer moves a step on. A second for the same asking - another client
// typing at the same time - is refused rather than taken for the next step.
func TestLogin_AStepIsAnsweredOnce(t *testing.T) {
	o, a := loginOwner(t, &stubConn{})
	answered := ask(o, internaltg.AuthRequest{Step: internaltg.AuthStepPhone})
	step, ok := recvAccount(t, a)
	require.True(t, ok)

	require.NoError(t, o.AnswerLogin(context.Background(), step.Login.Seq, "+100"))
	assert.Equal(t, "+100", (<-answered).Value)

	err := o.AnswerLogin(context.Background(), step.Login.Seq, "+200")
	assert.Equal(t, telerr.NotFound, telerr.Of(err))
}

// An answer naming an asking that is gone is refused even while another step
// waits: it was typed at a prompt that is no longer on screen.
func TestLogin_AnAnswerToAnOldAskingIsRefused(t *testing.T) {
	o, a := loginOwner(t, &stubConn{})
	answered := ask(o, internaltg.AuthRequest{Step: internaltg.AuthStepPhone})
	first, _ := recvAccount(t, a)
	require.NoError(t, o.AnswerLogin(context.Background(), first.Login.Seq, "+100"))
	<-answered
	ask(o, internaltg.AuthRequest{Step: internaltg.AuthStepCode})
	second, _ := recvAccount(t, a)

	err := o.AnswerLogin(context.Background(), first.Login.Seq, "12345")

	assert.Equal(t, telerr.NotFound, telerr.Of(err))
	assert.Greater(t, second.Login.Seq, first.Login.Seq)
}

func TestLogin_AFailureThatEndsTheLoginIsShown(t *testing.T) {
	o, a := loginOwner(t, &stubConn{})

	o.authFlow.Errors <- "Login requires email verification."

	got, ok := recvAccount(t, a)
	require.True(t, ok)
	assert.Equal(t, "Login requires email verification.", got.LoginFailed)
}

// Logged in, the account loads its chat list and is ready; the folders come
// with the chat list rather than on their own.
func TestLogin_LoggedInLoadsTheAccount(t *testing.T) {
	c := loginConn{folders: []domain.FolderFilter{{ID: 2, Title: "Work"}}}
	o, a := loginOwner(t, c)
	list := o.Attach()
	list.Subscribe(project.ChatListWindow{Limit: 10})

	close(o.readyCh)

	loading, ok := recvAccount(t, a)
	require.True(t, ok)
	assert.Equal(t, project.AccountLoading, loading.Phase)
	ready, ok := recvAccount(t, a)
	require.True(t, ok)
	assert.Equal(t, project.AccountReady, ready.Phase)
	require.Eventually(t, func() bool {
		for _, d := range only[project.Delta](queued(list)) {
			if d.ChatList != nil && d.ChatList.Kind == project.ChatListFolders && len(d.ChatList.Folders.Filters) == 1 {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond, "the folders reach the chat list")
}

// The login follows the account and ends with it: a login nobody finished must
// not hold the account's end up (#297).
func TestLogin_EndsWithTheAccount(t *testing.T) {
	o := New(&config.Config{}, zap.NewNop(), state.New(store.NewMemory()), &stubConn{}, nopNotifier{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		o.runLogin(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "the login outlived the account")
	}
}

// A connection that ends is shown to every client. One cancelled because the
// account is stopping, and a log out, are not: the first ended nothing worth
// saying, the second ends the account rather than the connection.
func TestOwner_WhatTheConnectionEndedWithIsTheAccounts(t *testing.T) {
	o := New(&config.Config{}, zap.NewNop(), state.New(store.NewMemory()), &stubConn{}, nopNotifier{})
	ctx := context.Background()

	o.connectionEnded(ctx, &telerr.Error{Kind: telerr.Unauthorized, LogOut: telerr.LogOutElsewhere})
	assert.Nil(t, o.Account().Ended, "a log out is the host's to act on")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	o.connectionEnded(cancelled, errors.New("context canceled"))
	assert.Nil(t, o.Account().Ended, "a stop is not a failure")

	failed := &telerr.Error{Kind: telerr.Network, Detail: "connection refused"}
	o.connectionEnded(ctx, failed)
	assert.Equal(t, error(failed), o.Account().Ended)
}
