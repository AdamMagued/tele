package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/store"
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
