package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/sorokin-vladimir/tele/internal/core/project"
)

// A window whose host is gone says why and how to carry on, instead of
// looking alive and receiving nothing; what was being typed is kept for the
// way out (#208).
func TestHostLost_TheWindowSaysWhyAndKeepsWhatWasTyped(t *testing.T) {
	for _, tt := range []struct {
		name string
		quit bool
		says string
	}{
		{"the host quit", true, "The tele that held this account has quit."},
		{"the connection broke", false, "The connection to the tele that held this account broke."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, m := openTestChat(t, project.HistoryContents{})
			next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			m = next.(RootModel)
			m.chat.SetComposerValue("half a thought")

			next, _ = m.Update(HostLostMsg{Quit: tt.quit})
			m = next.(RootModel)

			view := m.View().Content
			assert.Contains(t, view, tt.says)
			assert.Contains(t, view, "Start tele again")
			assert.Equal(t, "half a thought", m.Unsent())
		})
	}
}
