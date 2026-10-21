package dial

import (
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/sorokin-vladimir/tele/internal/ipc/protocol"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

// Reconnecting tells the client that its service went and is being waited
// for. It is this side's own, never sent by a host: nothing the client calls
// reaches anyone until Reconnected (#210).
type Reconnecting struct{}

// Reconnected tells the client that its service is back. What it subscribed
// to is sent whole again, as to a fresh subscription.
type Reconnected struct{}

// ErrGaveUp is a service that did not come back in time: something is wrong
// beyond a restart (#210).
var ErrGaveUp = errors.New("the service did not come back")

// How a client waits for its service: it tries again at once and then less and
// less often, up to every reattachPauseCap, and gives up after giveUpAfter.
var (
	reattachPauseCap = 5 * time.Second
	giveUpAfter      = 30 * time.Second
)

// firstPause is the wait before the second attempt.
const firstPause = 100 * time.Millisecond

// waitsForHost reports whether this client waits for its host when it goes:
// only a service is started again, so only a client of one does (#210).
func (a *Attachment) waitsForHost() bool {
	select {
	case <-a.closed:
		return false
	default:
	}
	return a.redial != nil && a.Host().Kept == wire.KeptByService
}

// reattach waits for the service to come back, and joins it: the next link and
// how to read it, or why it is given up on.
func (a *Attachment) reattach() (*link, *wire.Decoder, error) {
	if !a.emit(Reconnecting{}) {
		return nil, nil, hostLost()
	}
	// Closing the client calls off the wait, and a redial or hello in flight.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-a.closed:
			cancel()
		case <-ctx.Done():
		}
	}()

	deadline := time.Now().Add(a.giveUp)
	pause := min(firstPause, a.pauseCap)
	for {
		if conn, err := a.redial(ctx); err == nil {
			l, dec, err := a.rejoin(ctx, conn)
			if err == nil {
				return l, dec, nil
			}
			var mismatch *Mismatch
			if errors.As(err, &mismatch) {
				return nil, nil, mismatch
			}
		}
		if ctx.Err() != nil {
			return nil, nil, hostLost()
		}
		if time.Now().Add(pause).After(deadline) {
			return nil, nil, ErrGaveUp
		}
		select {
		case <-ctx.Done():
			return nil, nil, hostLost()
		case <-time.After(pause):
		}
		pause = min(2*pause, a.pauseCap)
	}
}

// rejoin says hello over conn to the host come back, and makes it this
// attachment's link: the subscriptions the client holds are made again, under
// the ids it chose, and what it showed last is said again.
func (a *Attachment) rejoin(ctx context.Context, conn io.ReadWriteCloser) (*link, *wire.Decoder, error) {
	host, dec, err := join(ctx, conn, a.hello)
	if err != nil {
		return nil, nil, err
	}
	l := &link{conn: conn, enc: wire.NewEncoder(conn), lost: make(chan struct{}), ready: make(chan struct{})}
	a.mu.Lock()
	a.host = host
	a.link = l
	subs := maps.Clone(a.subs)
	focus, focused := a.focus, a.focused
	a.mu.Unlock()

	// Said beside the reading rather than before it: the host answers each
	// subscription, and an answer nobody reads holds the host up, and this
	// with it.
	go func() {
		defer close(l.ready)
		for _, id := range slices.Sorted(maps.Keys(subs)) {
			a.tellOver(l, protocol.MethodSubscribe, id, subs[id])
		}
		if focused {
			a.tellOver(l, "SetFocus", focus)
		}
	}()
	if !a.emit(Reconnected{}) {
		return nil, nil, hostLost()
	}
	return l, dec, nil
}

// emit puts v on the stream, unless the client closes first.
func (a *Attachment) emit(v any) bool {
	select {
	case a.stream <- v:
		return true
	case <-a.closed:
		return false
	}
}
