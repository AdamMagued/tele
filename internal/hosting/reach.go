// Package hosting decides who serves a state directory's endpoint, and serves
// it: a process that finds a host there attaches to it, and one that finds
// nobody becomes the host (#208). The state directory lock arbitrates between
// processes starting at once, since a stale socket file does not stop a bind
// and binding alone could take the endpoint from a live host.
package hosting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/sorokin-vladimir/tele/internal/ipc"
	"github.com/sorokin-vladimir/tele/internal/statedir"
)

// Reached is what reaching a state directory's endpoint came to: a connection
// to the host already there, or the endpoint served by this process, which is
// the host now. Exactly one of Conn and Listener is set.
type Reached struct {
	Endpoint ipc.Endpoint
	// Conn is the connection to the host already there.
	Conn net.Conn
	// Listener serves the endpoint, and Lock holds the state directory, for as
	// long as this process is the host.
	Listener net.Listener
	Lock     *statedir.Lock
}

// Close gives up what was reached: the connection, or the endpoint and then
// the lock, in that order, so whoever starts next never finds the lock free
// and a socket still there.
func (r Reached) Close() {
	if r.Conn != nil {
		_ = r.Conn.Close()
	}
	if r.Listener != nil {
		_ = r.Listener.Close()
	}
	if r.Lock != nil {
		_ = r.Lock.Release()
	}
}

// Reach reaches the host of stateDir for the build named app, becoming it when
// there is none.
func Reach(ctx context.Context, app, stateDir string) (Reached, error) {
	// The endpoint is named after the state directory's resolved path, which
	// needs the directory to exist; the lock would make it a moment later.
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return Reached{}, fmt.Errorf("create state dir: %w", err)
	}
	ep, err := ipc.EndpointFor(app, stateDir)
	if err != nil {
		return Reached{}, err
	}
	conn, err := ipc.Dial(ctx, ep)
	if err == nil {
		return Reached{Endpoint: ep, Conn: conn}, nil
	}
	if !errors.Is(err, ipc.ErrNoHost) {
		return Reached{}, err
	}

	lock, err := statedir.Acquire(stateDir)
	if errors.Is(err, statedir.ErrLocked) {
		// Someone holds the state directory: a host still starting, or one that
		// will never serve. The first is waited for.
		return waitFor(ctx, ep, err)
	}
	if err != nil {
		return Reached{}, err
	}
	if err := ipc.RemoveStale(ep); err != nil {
		_ = lock.Release()
		return Reached{}, fmt.Errorf("removing a stale socket: %w", err)
	}
	l, err := ipc.Listen(ep)
	if err != nil {
		_ = lock.Release()
		return Reached{}, err
	}
	return Reached{Endpoint: ep, Listener: l, Lock: lock}, nil
}

// ErrHeldNotServing is a state directory another process holds without
// serving its endpoint: an older build, which never serves one, or a process
// stuck before it got that far.
var ErrHeldNotServing = errors.New("another tele holds the state directory and does not accept connections")

// waitForHost is how long a host that holds the lock is given to serve.
var waitForHost = 5 * time.Second

// waitFor dials ep until the host holding the lock serves it, backing off
// between tries, and gives up after waitForHost. locked is what taking the
// lock said, which names the process holding it.
func waitFor(ctx context.Context, ep ipc.Endpoint, locked error) (Reached, error) {
	deadline := time.Now().Add(waitForHost)
	pause := 50 * time.Millisecond
	for {
		conn, err := ipc.Dial(ctx, ep)
		if err == nil {
			return Reached{Endpoint: ep, Conn: conn}, nil
		}
		if !errors.Is(err, ipc.ErrNoHost) {
			return Reached{}, err
		}
		if time.Now().Add(pause).After(deadline) {
			return Reached{}, fmt.Errorf("%w (%v)", ErrHeldNotServing, locked)
		}
		select {
		case <-ctx.Done():
			return Reached{}, ctx.Err()
		case <-time.After(pause):
		}
		pause = min(2*pause, 500*time.Millisecond)
	}
}
