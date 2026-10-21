package app

import (
	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

// AccountStatus is how the account running now is doing, in the protocol's
// words, for whoever asks the host rather than draws the account (#208). Nil
// between one account and the next.
func (h *Host) AccountStatus() *wire.AccountStatus {
	acct := h.current()
	if acct == nil {
		return nil
	}
	o := acct.owner
	a, health := o.Account(), o.Health()
	s := &wire.AccountStatus{
		Phase:      phases[a.Phase],
		User:       a.LoggedInUser.Username,
		Link:       links[health.Link],
		LinkSince:  health.LinkSince,
		LastUpdate: health.LastUpdate,
		Outbox:     o.UnsentCount(),
	}
	if a.Ended != nil {
		s.Ended = a.Ended.Error()
	}
	return s
}

var phases = map[project.AccountPhase]wire.Phase{
	project.AccountConnecting: wire.PhaseConnecting,
	project.AccountLoggingIn:  wire.PhaseLoggingIn,
	project.AccountLoading:    wire.PhaseLoading,
	project.AccountReady:      wire.PhaseReady,
}

var links = map[core.Link]wire.Link{
	core.LinkUnknown: wire.LinkUnknown,
	core.LinkUp:      wire.LinkUp,
	core.LinkDown:    wire.LinkDown,
}
