package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/core/state"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// Clock skew is a state rather than a stream: a client wants where the clock
// stands, not every value it missed, and the report comes from gotd's
// connection, which must never wait for a client (#277).
func TestOwner_ClockSkewKeepsOnlyTheLatest(t *testing.T) {
	o := New(&config.Config{}, zap.NewNop(), state.New(store.NewMemory()), &stubConn{}, nopNotifier{})

	o.SetClockSkew(8 * time.Minute)
	o.SetClockSkew(9 * time.Minute)
	o.SetClockSkew(0)

	assert.Zero(t, o.Account().ClockSkew)
}

// A client that attaches while the clock is skewed sees the skew at once: it
// is state, and an event sent before the client was there would be lost on it
// (#309).
func TestOwner_AClientAttachingDuringASkewSeesIt(t *testing.T) {
	o := New(&config.Config{}, zap.NewNop(), state.New(store.NewMemory()), &stubConn{}, nopNotifier{})
	o.SetClockSkew(8 * time.Minute)

	c := o.Attach()
	c.Subscribe(project.AccountWindow{})

	d, ok := recvDelta(t, c)
	require.True(t, ok)
	require.NotNil(t, d.Account)
	assert.Equal(t, 8*time.Minute, d.Account.Contents.ClockSkew)
}
