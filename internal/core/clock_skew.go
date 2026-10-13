package core

import (
	"time"

	"github.com/sorokin-vladimir/tele/internal/core/project"
)

// SetClockSkew records how far the local clock is from Telegram's, when it is
// far enough that the messages between them are refused (#277); zero when
// there is no skew, or no longer is. Positive means the local clock is ahead.
//
// A skew is state, so it lives in the account rather than travelling as an
// event: a client attaching while the clock is skewed has to see it (#309). It
// never blocks: it is called from gotd's connection, which must not wait for a
// client.
func (o *Owner) SetClockSkew(d time.Duration) {
	o.updateAccount(func(c *project.AccountContents) { c.ClockSkew = d })
}
