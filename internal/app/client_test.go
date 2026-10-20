package app

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/ui"
)

// streamOnly is an attachment the test feeds by hand.
type streamOnly struct {
	Attachment
	stream chan any
}

func (s streamOnly) Stream() <-chan any { return s.stream }

// bridged runs a client's bridge over a stream the test feeds, and hands
// back what reached the model.
func bridged(t *testing.T, feed func(chan any)) []any {
	t.Helper()
	stream := make(chan any, 4)
	got := make(chan any, 8)
	c := &Client{c: ClientConfig{Log: zap.NewNop(), Attachment: streamOnly{stream: stream}}}
	c.send = func(msg tea.Msg) { got <- msg }
	done := make(chan struct{})
	go func() {
		c.bridge(context.Background())
		close(done)
	}()
	feed(stream)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the bridge outlived the stream")
	}
	close(got)
	var all []any
	for msg := range got {
		all = append(all, msg)
	}
	return all
}

// A host that said it was stopping before the link went has quit; a link that
// went without a word broke. The window is told which (#208).
func TestClient_ALostHostIsToldApartFromABrokenOne(t *testing.T) {
	quit := bridged(t, func(s chan any) {
		s <- core.HostStopping{}
		close(s)
	})
	broke := bridged(t, func(s chan any) { close(s) })

	assert.Equal(t, []any{ui.HostLostMsg{Quit: true}}, quit)
	assert.Equal(t, []any{ui.HostLostMsg{Quit: false}}, broke)
}
