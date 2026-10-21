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
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/ui"
)

// streamOnly is an attachment the test feeds by hand, which ended for lost.
type streamOnly struct {
	Attachment
	stream chan any
	lost   error
}

func (s streamOnly) Stream() <-chan any { return s.stream }
func (s streamOnly) Lost() error        { return s.lost }

// bridged runs a client's bridge over a stream the test feeds, and hands
// back what reached the model.
func bridged(t *testing.T, feed func(chan any)) []any {
	t.Helper()
	return bridgedLost(t, nil, feed)
}

// bridgedLost is bridged over an attachment that ended for lost.
func bridgedLost(t *testing.T, lost error, feed func(chan any)) []any {
	t.Helper()
	stream := make(chan any, 4)
	got := make(chan any, 8)
	c := &Client{c: ClientConfig{Log: zap.NewNop(), Attachment: streamOnly{stream: stream, lost: lost}}}
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

// A service that said it was stopping and came back has not quit: its
// stopping belongs to the restart, and whatever ends the link later is told
// as it is. The model hears the wait and the return (#210).
func TestClient_AServiceThatCameBackHasNotQuit(t *testing.T) {
	got := bridged(t, func(s chan any) {
		s <- core.HostStopping{Reason: core.StopRestart}
		s <- dial.Reconnecting{}
		s <- dial.Reconnected{}
		close(s)
	})

	assert.Equal(t, []any{ui.HostAwayMsg{}, ui.HostBackMsg{}, ui.HostLostMsg{Quit: false}}, got)
}

// A service that stopped because it was uninstalled is told as such: the
// window has nothing to wait for (#182).
func TestClient_AnUninstalledServiceIsTold(t *testing.T) {
	got := bridged(t, func(s chan any) {
		s <- core.HostStopping{Reason: core.StopUninstalled}
		close(s)
	})

	assert.Equal(t, []any{ui.HostLostMsg{Quit: true, Uninstalled: true}}, got)
}

// A service given up on, or one that came back newer than this window, is
// told as such, for the window to say so (#210, #183).
func TestClient_WhyTheServiceWasLostIsTold(t *testing.T) {
	gaveUp := bridgedLost(t, dial.ErrGaveUp, func(s chan any) {
		s <- dial.Reconnecting{}
		close(s)
	})
	updated := bridgedLost(t, &dial.Mismatch{Verdict: wire.RestartClient, Host: wire.Hello{Binary: "2.1.0"}}, func(s chan any) {
		s <- dial.Reconnecting{}
		close(s)
	})

	assert.Equal(t, []any{ui.HostAwayMsg{}, ui.HostLostMsg{GaveUp: true}}, gaveUp)
	assert.Equal(t, []any{ui.HostAwayMsg{}, ui.HostLostMsg{UpdatedTo: "2.1.0"}}, updated)
}
