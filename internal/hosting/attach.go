package hosting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/sorokin-vladimir/tele/internal/ipc"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

// ErrServiceStillOlder is a service that came back from a restart on the
// binary it ran before: the unit names a file the upgrade did not replace. It
// is not asked again, which would only restart it in a loop (#183).
var ErrServiceStillOlder = errors.New("the service started again on the version it ran before")

// ErrNoHostCameBack is a host that went and did not come back in time.
var ErrNoHostCameBack = errors.New("no tele came back to host the state directory")

// waitForService is how long a client waits for a service to come back.
var waitForService = dial.ServiceReturn

// Attach attaches to the host r reached, saying hello. A service older than
// this client is asked to restart, and attached to once its service manager
// has started it again; a client never becomes a host while it waits. Any
// other host this client cannot use answers with a *dial.Mismatch (#183).
func Attach(ctx context.Context, r Reached, hello wire.Hello) (*dial.Attachment, error) {
	// A client of a service waits for it when it goes, reaching it again at
	// the same endpoint (#210).
	redial := dial.Redial(func(ctx context.Context) (io.ReadWriteCloser, error) {
		return ipc.Dial(ctx, r.Endpoint)
	})
	att, err := dial.Dial(ctx, r.Conn, hello, redial)
	var mismatch *dial.Mismatch
	if !errors.As(err, &mismatch) || mismatch.Verdict != wire.RestartHost {
		return att, err
	}
	old := mismatch.Host.Binary

	conn, err := ipc.Dial(ctx, r.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("reaching the service to restart it: %w", err)
	}
	answer, err := dial.AskRestart(ctx, conn, hello)
	if err != nil {
		return nil, fmt.Errorf("asking the service (%s) to restart: %w", old, err)
	}
	if answer.Refused != "" {
		return nil, fmt.Errorf("the service (%s) would not restart: %s", old, answer.Refused)
	}

	conn, err = WaitForHost(ctx, r.Endpoint)
	if err != nil {
		return nil, err
	}
	att, err = dial.Dial(ctx, conn, hello, redial)
	if errors.As(err, &mismatch) && mismatch.Verdict == wire.RestartHost {
		return nil, fmt.Errorf("%w: it runs %s", ErrServiceStillOlder, mismatch.Host.Binary)
	}
	return att, err
}

// WaitForHost dials ep until a host answers there, backing off between tries,
// and gives up after waitForService. It never takes the state directory: a
// client waiting for a service does not become a host, however long it waits.
func WaitForHost(ctx context.Context, ep ipc.Endpoint) (net.Conn, error) {
	conn, err := dialUntil(ctx, ep, waitForService)
	if errors.Is(err, errGaveUp) {
		return nil, ErrNoHostCameBack
	}
	return conn, err
}

// errGaveUp is dialUntil giving up.
var errGaveUp = errors.New("gave up")

// dialUntil dials ep until a host answers there, backing off between tries,
// and gives up with errGaveUp after within.
func dialUntil(ctx context.Context, ep ipc.Endpoint, within time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(within)
	pause := 50 * time.Millisecond
	for {
		conn, err := ipc.Dial(ctx, ep)
		if err == nil {
			return conn, nil
		}
		if !errors.Is(err, ipc.ErrNoHost) {
			return nil, err
		}
		if time.Now().Add(pause).After(deadline) {
			return nil, errGaveUp
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pause):
		}
		pause = min(2*pause, 500*time.Millisecond)
	}
}
