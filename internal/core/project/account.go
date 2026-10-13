package project

import "time"

// AccountWindow is the account as a whole: how far it has got since its owner
// started, the login step it is waiting on, how its connection ended, and the
// clock. There is one of it, so the window names nothing (#309).
type AccountWindow struct{}

func (AccountWindow) isWindow() {}

// AccountPhase is how far an account has got since its owner started.
type AccountPhase int

const (
	// AccountConnecting means nothing has been heard from Telegram yet.
	AccountConnecting AccountPhase = iota
	// AccountLoggingIn means Telegram is waiting on a step of the login; the
	// contents' Login says which.
	AccountLoggingIn
	// AccountLoading means the account is logged in and its chat list is being
	// loaded.
	AccountLoading
	// AccountReady means the chat list is loaded.
	AccountReady
)

// LoginStepKind names a step of the login.
type LoginStepKind int

const (
	LoginPhone LoginStepKind = iota
	LoginCode
	LoginPassword
)

// LoginStep is the step of the login waiting for an answer. It is state, not a
// question put to one client: whoever attaches while it waits sees it, and
// whoever answers first moves it on. It carries no secret, only what the
// screen says about the step.
type LoginStep struct {
	Kind LoginStepKind
	// Hint is how the code was sent, for the code step.
	Hint string
	// Err says why the step is asked again: the answer given last time was
	// refused. Empty the first time (#285).
	Err string
	// Seq tells one asking of a step from the next, so an answer names the
	// asking it answers and one for an asking already gone is refused.
	Seq int
}

// AccountContents is everything an account subscription shows.
type AccountContents struct {
	Phase AccountPhase
	// Login is the step waiting for an answer, while Phase is AccountLoggingIn.
	Login LoginStep
	// LoginFailed says, in full, why the login cannot go on from here; empty
	// while it can.
	LoginFailed string
	// Ended is what the connection ended with, nil while it runs.
	Ended error
	// ClockSkew is how far the local clock is from Telegram's, when it is far
	// enough that messages between them are refused; zero otherwise (#277).
	ClockSkew time.Duration
}

// AccountDelta carries the account whole: it is small, and a part of it
// means little without the rest.
type AccountDelta struct {
	Contents AccountContents
}

// BuildAccount answers the account window.
func BuildAccount(r Reader) AccountContents { return r.Account() }

// sameAccount reports whether two contents show the same thing. Errors are
// compared by what they say: an error is not always comparable, and two that
// say the same are the same to whoever reads them.
func sameAccount(a, b AccountContents) bool {
	if a.Phase != b.Phase || a.Login != b.Login || a.LoginFailed != b.LoginFailed || a.ClockSkew != b.ClockSkew {
		return false
	}
	if (a.Ended == nil) != (b.Ended == nil) {
		return false
	}
	return a.Ended == nil || a.Ended.Error() == b.Ended.Error()
}
