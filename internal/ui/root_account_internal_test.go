package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// What is typed at a login step goes to the owner naming the asking it
// answers, so the owner can refuse it if another client got there first
// (#309).
func TestLogin_AnAnswerReachesTheOwnerWithItsAsking(t *testing.T) {
	o := newOwnerStub(store.NewMemory())
	m := nextAccountRoot().WithOwner(o)
	m, _ = m.handleAccountDelta(project.AccountContents{
		Phase: project.AccountLoggingIn,
		Login: project.LoginStep{Kind: project.LoginPhone, Seq: 7},
	})

	next, _ := m.Update(tea.PasteMsg{Content: "+100"})
	_, cmd := next.(RootModel).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	answer, ok := cmd().(screens.LoginAnswerMsg)
	require.True(t, ok)
	_, send := next.(RootModel).Update(answer)
	require.NotNil(t, send)
	send()

	assert.Equal(t, []screens.LoginAnswerMsg{{Seq: 7, Value: "+100"}}, o.answers)
}

// An answer refused because another window drives the login says so under
// the field: the person typing here would otherwise wait on nothing (#211).
func TestLogin_AnAnswerRefusedForAnotherWindowSaysSo(t *testing.T) {
	o := newOwnerStub(store.NewMemory())
	o.answerErr = &telerr.Error{Kind: telerr.Forbidden, Op: "login", Detail: "this login is being answered in another window"}
	m := nextAccountRoot().WithOwner(o)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = next.(RootModel).handleAccountDelta(project.AccountContents{
		Phase: project.AccountLoggingIn,
		Login: project.LoginStep{Kind: project.LoginCode, Seq: 3},
	})

	_, send := m.Update(screens.LoginAnswerMsg{Seq: 3, Value: "12345"})
	require.NotNil(t, send)
	next, _ = m.Update(send())

	assert.Contains(t, next.(RootModel).View().Content, "This login is being answered in another window.")
}
