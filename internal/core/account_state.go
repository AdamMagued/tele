package core

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

// accountState is where the account stands, as the account projection shows
// it. It is state rather than a stream of happenings: a client attaching in the
// middle of a login, or while the clock is skewed, sees the step and the skew,
// not nothing (#309).
type accountState struct {
	mu sync.Mutex
	c  project.AccountContents
	// waiting is set while the step in c.Login has not been answered. One
	// answer moves it on; a second for the same asking is refused.
	waiting bool
}

// Account is where the account stands now.
func (o *Owner) Account() project.AccountContents {
	o.acct.mu.Lock()
	defer o.acct.mu.Unlock()
	return o.acct.c
}

// updateAccount changes where the account stands and tells every client
// watching it.
func (o *Owner) updateAccount(fn func(c *project.AccountContents)) {
	o.acct.mu.Lock()
	fn(&o.acct.c)
	o.acct.mu.Unlock()
	o.Refresh()
}

// runLogin follows the login from the connection into the account state, and
// once the account is logged in loads its chat list and its folders. It ends
// with the login failing for good, with the chat list loaded, or with ctx.
func (o *Owner) runLogin(ctx context.Context) {
	af := o.authFlow
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-af.Requests:
			o.log.Debug("auth step requested", zap.Int("step", int(req.Step)))
			o.updateAccount(func(c *project.AccountContents) {
				c.Phase = project.AccountLoggingIn
				c.Login = project.LoginStep{Kind: loginStepKind(req.Step), Hint: req.Hint, Err: req.Err, Seq: c.Login.Seq + 1}
				o.acct.waiting = true
			})
		case text := <-af.Errors:
			o.log.Error("auth error", zap.String("reason", text))
			o.updateAccount(func(c *project.AccountContents) { c.LoginFailed = text })
			return
		case <-o.readyCh:
			o.log.Info("connected, loading dialogs")
			o.updateAccount(func(c *project.AccountContents) {
				c.Phase = project.AccountLoading
				c.Login = project.LoginStep{Seq: c.Login.Seq}
				o.acct.waiting = false
			})
			o.loadAccount(ctx)
			return
		}
	}
}

// loadAccount loads the chat list and, alongside it, the folders. The account
// is ready once the chat list is in: folders follow on their own, and the
// cached ones are on screen until they do.
func (o *Owner) loadAccount(ctx context.Context) {
	o.spawn(func() {
		if _, err := o.LoadFolderFilters(ctx); err != nil {
			o.log.Warn("GetDialogFilters failed", zap.Error(err))
			return
		}
		// Folders are written without a change to commit, so the chat lists
		// on screen are rebuilt here.
		o.Refresh()
	})
	if err := o.Bootstrap(ctx); err != nil {
		o.log.Error("GetDialogs failed", zap.Error(err))
		return
	}
	o.updateAccount(func(c *project.AccountContents) { c.Phase = project.AccountReady })
}

// loggedIn records who the account is logged in as, for every client watching
// it.
func (o *Owner) loggedIn(userID int64, username string) {
	o.updateAccount(func(c *project.AccountContents) {
		c.LoggedInUser = project.LoggedInUser{ID: userID, Username: username}
	})
}

// AnswerLogin answers the step of the login named by seq. An answer for an
// asking that is no longer the one waiting - another client answered first,
// or the step moved on - is refused, so two clients typing at once cannot
// answer one step twice.
func (o *Owner) AnswerLogin(ctx context.Context, seq int, value string) error {
	o.acct.mu.Lock()
	if !o.acct.waiting || o.acct.c.Login.Seq != seq {
		o.acct.mu.Unlock()
		return &telerr.Error{Kind: telerr.NotFound, Op: "login", Detail: "this login step was already answered"}
	}
	o.acct.waiting = false
	o.acct.mu.Unlock()
	select {
	case o.authFlow.Responses <- internaltg.AuthResponse{Value: value}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// connectionEnded records what the connection ended with, for every client to
// see. A connection cancelled because the account is stopping ended with
// nothing worth saying, and a log out is the end of the account rather than of
// a connection: the host ends it and starts the next (#297).
func (o *Owner) connectionEnded(ctx context.Context, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}
	if _, loggedOut := EndReasonOf(err); loggedOut {
		return
	}
	o.updateAccount(func(c *project.AccountContents) { c.Ended = telerr.From(err) })
}

func loginStepKind(s internaltg.AuthStep) project.LoginStepKind {
	switch s {
	case internaltg.AuthStepCode:
		return project.LoginCode
	case internaltg.AuthStepPassword:
		return project.LoginPassword
	default:
		return project.LoginPhone
	}
}
