package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// awayChat is a window on an open chat, sized, with how long the host may be
// away before the window says so shortened to nothing.
func awayChat(t *testing.T) RootModel {
	t.Helper()
	was := hostAwayAfter
	hostAwayAfter = time.Millisecond
	t.Cleanup(func() { hostAwayAfter = was })
	_, m := openTestChat(t, project.HistoryContents{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return next.(RootModel)
}

// update is m after msg, and the command it gave.
func update(m RootModel, msg tea.Msg) (RootModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(RootModel), cmd
}

const awayWords = "host unreachable, reconnecting"

// A host that is away for a moment is not worth a word; one away for longer
// is named in the status bar until it is back (#210).
func TestHostAway_TheStatusBarSaysSoAfterAWhile(t *testing.T) {
	m := awayChat(t)

	m, tick := update(m, HostAwayMsg{})
	assert.NotContains(t, m.View().Content, awayWords, "said at once")
	require.NotNil(t, tick)

	m, _ = update(m, tick())
	assert.Contains(t, m.View().Content, awayWords)

	m, _ = update(m, HostBackMsg{})
	assert.NotContains(t, m.View().Content, awayWords)
}

// A host back before the window would have said anything is never named: the
// wait's timer belongs to the absence it was started for.
func TestHostAway_AHostBackInTimeIsNeverNamed(t *testing.T) {
	m := awayChat(t)

	m, tick := update(m, HostAwayMsg{})
	m, _ = update(m, HostBackMsg{})
	m, _ = update(m, tick())

	assert.NotContains(t, m.View().Content, awayWords)
}

// A host gone again is a new absence: the timer of the last one does not name
// it before its own moment has passed.
func TestHostAway_ALaterAbsenceWaitsForItsOwnTimer(t *testing.T) {
	m := awayChat(t)

	m, first := update(m, HostAwayMsg{})
	m, _ = update(m, HostBackMsg{})
	m, second := update(m, HostAwayMsg{})
	m, _ = update(m, first())
	assert.NotContains(t, m.View().Content, awayWords, "the last absence's timer named this one")

	m, _ = update(m, second())
	assert.Contains(t, m.View().Content, awayWords)
}

// Enter while the host is away tells the person what was typed is kept.
func TestHostAway_EnterSaysTheTextIsKept(t *testing.T) {
	m := awayChat(t)
	m, _ = update(m, HostAwayMsg{})

	m, _ = update(m, screens.NotSentWhileAwayMsg{})

	assert.Contains(t, onScreen(m), "kept")
}

// The chat hears the host is away and back, which is what decides Enter.
func TestHostAway_TheChatHearsIt(t *testing.T) {
	m := awayChat(t)
	m.chat.SetComposerValue("hello")
	m.chat.FocusComposer()

	m, _ = update(m, HostAwayMsg{})
	_, cmd := m.chat.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.IsType(t, screens.NotSentWhileAwayMsg{}, cmd())

	m, _ = update(m, HostBackMsg{})
	_, cmd = m.chat.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.IsType(t, screens.SendMsgRequest{}, cmd())
}

// A service given up on is the same dead end as any lost host, and says where
// to look (#210).
func TestHostLost_AServiceGivenUpOnPointsToStatus(t *testing.T) {
	m := awayChat(t)

	m, _ = update(m, HostLostMsg{GaveUp: true})

	view := m.View().Content
	assert.Contains(t, view, "tele daemon status")
	assert.Contains(t, view, "Start tele again")
}

// A service that came back newer than this window turns it away: tele was
// updated (#183).
func TestHostLost_AnUpdatedServiceSaysTeleWasUpdated(t *testing.T) {
	m := awayChat(t)

	m, _ = update(m, HostLostMsg{UpdatedTo: "2.1.0"})

	view := m.View().Content
	assert.Contains(t, view, "tele was updated")
	assert.Contains(t, view, "2.1.0")
}
