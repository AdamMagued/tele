package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// handleAccountDelta draws where the account stands. The contents arrive whole,
// and the screen acts on what changed since the last: a login step asked, a
// login that cannot go on, the account logged in and then loaded, a connection
// that ended, the clock. The first contents are compared with a fresh start, so
// a client attaching in the middle of a login is asked the step at once (#309).
func (m RootModel) handleAccountDelta(next project.AccountContents) (RootModel, tea.Cmd) {
	prev := m.account
	m.account = next
	var cmds []tea.Cmd
	route := func(msg tea.Msg) {
		updated, cmd := m.updateInner(msg)
		m = updated.(RootModel)
		cmds = append(cmds, cmd)
	}

	if next.ClockSkew != prev.ClockSkew {
		m = m.handleClockSkew(next.ClockSkew)
	}
	if next.Phase == project.AccountLoggingIn && next.Login != prev.Login {
		route(screens.AuthRequestMsg{Step: next.Login.Kind, Hint: next.Login.Hint, Err: next.Login.Err, Seq: next.Login.Seq})
	}
	if next.LoginFailed != "" && next.LoginFailed != prev.LoginFailed {
		route(screens.AuthErrorMsg{Text: next.LoginFailed})
	}
	if next.Phase >= project.AccountLoading && prev.Phase < project.AccountLoading {
		route(screens.ConnectedMsg{})
	}
	if next.Phase == project.AccountReady && prev.Phase != project.AccountReady {
		route(screens.TransitionToMainMsg{})
	}
	if next.Ended != nil && (prev.Ended == nil || prev.Ended.Error() != next.Ended.Error()) {
		updated, cmd := m.handleConnectFailed(next.Ended)
		m = updated
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleLoginAnswer hands what was typed at a login step to the owner. A
// refusal says the step was already answered, here or by another client, and
// the account projection is already on its way with whatever comes next; any
// other failure is the account ending, which arrives the same way. Either way
// there is nothing to say here.
func (m RootModel) handleLoginAnswer(msg screens.LoginAnswerMsg) (RootModel, tea.Cmd) {
	if m.owner == nil {
		return m, nil
	}
	ctx, owner := m.ctx, m.owner
	return m, func() tea.Msg {
		_ = owner.AnswerLogin(ctx, msg.Seq, msg.Value)
		return nil
	}
}
