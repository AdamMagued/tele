package screens_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// typing is a chat open on a user, with text in its focused composer.
func typing(t *testing.T, text string) *screens.ChatModel {
	t.Helper()
	m := screens.NewChatModel(80, 24)
	openChat(m, &domain.Chat{ID: 10, Peer: domain.Peer{ID: 10, Type: domain.PeerUser}})
	next, _ := m.Update(keys.ActionMsg{Action: keys.ActionInsert})
	m = next.(*screens.ChatModel)
	m.SetComposerValue(text)
	return m
}

// While the host is away, Enter sends nothing and keeps what was typed: the
// person is told so, and sends it once the host is back (#210).
func TestChat_EnterWhileTheHostIsAwayKeepsTheText(t *testing.T) {
	m := typing(t, "hello")
	m.SetHostAway(true)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*screens.ChatModel)

	require.NotNil(t, cmd)
	assert.IsType(t, screens.NotSentWhileAwayMsg{}, cmd())
	assert.Equal(t, "hello", m.ComposerValue())
}

// Staged files wait as the text does.
func TestChat_EnterWhileTheHostIsAwayKeepsTheAttachment(t *testing.T) {
	m := typing(t, "caption")
	m.SetAttachment("pic.jpg", 1000, domain.MediaPhoto, domain.MediaPhoto, true)
	m.SetHostAway(true)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*screens.ChatModel)

	require.NotNil(t, cmd)
	assert.IsType(t, screens.NotSentWhileAwayMsg{}, cmd())
	assert.True(t, m.HasAttachment())
}

// Once the host is back, Enter sends as always.
func TestChat_EnterOnceTheHostIsBackSends(t *testing.T) {
	m := typing(t, "hello")
	m.SetHostAway(true)
	m.SetHostAway(false)

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	assert.IsType(t, screens.SendMsgRequest{}, cmd())
}
