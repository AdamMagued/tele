package screens_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

func TestLogin_AuthRequest_UpdatesPrompt(t *testing.T) {
	m := screens.NewLoginModel()
	newM, _ := m.Update(screens.AuthRequestMsg{Step: project.LoginPhone})
	lm := newM.(screens.LoginModel)
	assert.Equal(t, project.LoginPhone, lm.CurrentStep())
	assert.Contains(t, lm.View().Content, "phone")
}

func TestLogin_Connected_EmitsTransition(t *testing.T) {
	m := screens.NewLoginModel()
	_, cmd := m.Update(screens.ConnectedMsg{})
	require.NotNil(t, cmd)
	msg := cmd()
	assert.IsType(t, screens.TransitionToMainMsg{}, msg)
}

func TestLogin_InitialView_ShowsConnecting(t *testing.T) {
	m := screens.NewLoginModel()
	assert.Contains(t, m.View().Content, "onnect") // "Connecting..." or "Connect"
}

// The slow mark belongs to the wait for Telegram's first answer. Once a prompt
// is up the wait is over, and a tick that arrives late must not bring it back.
func TestLogin_SlowConnectMarksOnlyTheWait(t *testing.T) {
	m := screens.NewLoginModel()
	assert.False(t, m.Slow())

	slow, _ := m.Update(screens.SlowConnectMsg{})
	assert.True(t, slow.(screens.LoginModel).Slow())

	prompted, _ := m.Update(screens.AuthRequestMsg{Step: project.LoginPhone})
	late, _ := prompted.(screens.LoginModel).Update(screens.SlowConnectMsg{})
	assert.False(t, late.(screens.LoginModel).Slow())
}

// A prompt arriving after the mark ends the wait too.
func TestLogin_PromptEndsTheSlowWait(t *testing.T) {
	m := screens.NewLoginModel()
	slow, _ := m.Update(screens.SlowConnectMsg{})

	prompted, _ := slow.(screens.LoginModel).Update(screens.AuthRequestMsg{Step: project.LoginPhone})

	assert.False(t, prompted.(screens.LoginModel).Slow())
}

// A step asked again says why under the field, and the reason goes away with
// the next step that has none (#285).
func TestLogin_ReasonShowsUnderTheField(t *testing.T) {
	m := screens.NewLoginModel()

	again, _ := m.Update(screens.AuthRequestMsg{Step: project.LoginCode, Err: "That code is not right. Try again."})
	assert.Contains(t, again.(screens.LoginModel).View().Content, "That code is not right. Try again.")

	onward, _ := again.(screens.LoginModel).Update(screens.AuthRequestMsg{Step: project.LoginPassword})
	assert.NotContains(t, onward.(screens.LoginModel).View().Content, "not right")
}

// submit presses enter on the login screen and returns the answer it gave.
func submit(t *testing.T, m screens.LoginModel) screens.LoginAnswerMsg {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "nothing was submitted")
	answer, ok := cmd().(screens.LoginAnswerMsg)
	require.True(t, ok, "enter answers the step")
	return answer
}

func typed(m screens.LoginModel, s string) screens.LoginModel {
	next, _ := m.Update(tea.PasteMsg{Content: s})
	return next.(screens.LoginModel)
}

// Copying a number or a code tends to bring a space or a line break with it,
// and Telegram would refuse the value for it (#284).
func TestLogin_PhoneAndCodeAreTrimmed(t *testing.T) {
	for _, step := range []project.LoginStepKind{project.LoginPhone, project.LoginCode} {
		m, _ := screens.NewLoginModel().Update(screens.AuthRequestMsg{Step: step})

		assert.Equal(t, "12345", submit(t, typed(m.(screens.LoginModel), " 12345 ")).Value)
	}
}

// A password is sent exactly as typed: a space may be part of it, and Telegram
// trims nothing.
func TestLogin_PasswordIsNotTrimmed(t *testing.T) {
	m, _ := screens.NewLoginModel().Update(screens.AuthRequestMsg{Step: project.LoginPassword})

	assert.Equal(t, " secret ", submit(t, typed(m.(screens.LoginModel), " secret ")).Value)
}

// An answer names the asking it answers, so the owner can refuse one for a
// step another client has already answered (#309).
func TestLogin_AnswerNamesTheAskingItAnswers(t *testing.T) {
	m, _ := screens.NewLoginModel().Update(screens.AuthRequestMsg{Step: project.LoginCode, Seq: 3})

	assert.Equal(t, 3, submit(t, typed(m.(screens.LoginModel), "12345")).Seq)
}

// A notice says why the person is at the login. It is shown under the number
// the first time the number is asked for, and gives way to a reason the number
// is asked again (#297).
func TestLogin_NoticeShowsUnderTheFirstNumberStep(t *testing.T) {
	m := screens.NewLoginModel()
	m.SetNotice("This session was logged out.")

	first, _ := m.Update(screens.AuthRequestMsg{Step: project.LoginPhone})
	assert.Contains(t, first.View().Content, "This session was logged out.")

	again, _ := first.Update(screens.AuthRequestMsg{Step: project.LoginPhone, Err: "That number is not valid."})
	assert.NotContains(t, again.View().Content, "This session was logged out.")
	assert.Contains(t, again.View().Content, "That number is not valid.")
}

// ensure tea import is used (Blink cmd returns tea.Cmd)
var _ tea.Cmd = screens.NewLoginModel().Init()
