package ui

import tea "charm.land/bubbletea/v2"

// HostLostMsg says the client's link to its host is gone. Quit is set when the
// host said it was stopping first - the tele that held the account quit -
// and clear when the connection simply broke (#208).
type HostLostMsg struct {
	Quit bool
}

// handleHostLost leaves the window with nothing to show but why: a window
// that looks alive and receives nothing is the failure the whole design exists
// to avoid. What was being typed is kept, so the way out can hand it back.
func (m RootModel) handleHostLost(msg HostLostMsg) (RootModel, tea.Cmd) {
	if m.screen == ScreenMain {
		m.unsent = m.chat.ComposerValue()
	}
	cause := "The connection to the tele that held this account broke."
	if msg.Quit {
		cause = "The tele that held this account has quit."
	}
	cause += "\nStart tele again to carry on."
	m.screen = ScreenLogin
	return m.routeLoginError(m.loginErrorText(cause, ""))
}

// Unsent is what was being typed when the host was lost, empty otherwise.
func (m RootModel) Unsent() string { return m.unsent }
