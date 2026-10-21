package dial_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/serve"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// service says what a service of binary says of itself.
func service(binary string) wire.Hello {
	h := hello
	h.Binary, h.Kept = binary, wire.KeptByService
	return h
}

// hostOn serves rec over a fresh connection, saying said, and gives the
// client's end of it and what drops it, as a host that goes away does.
func hostOn(t *testing.T, rec *recorder, said wire.Hello) (net.Conn, func()) {
	t.Helper()
	client, end := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = serve.Conn(ctx, end, rec, &hostEnd{hello: said})
	}()
	drop := func() {
		cancel()
		<-served
	}
	t.Cleanup(drop)
	return client, drop
}

// comingBack is a host the client redials: the connections put on it are the
// host back, and with none there nobody answers yet.
type comingBack chan net.Conn

func (c comingBack) redial(context.Context) (io.ReadWriteCloser, error) {
	select {
	case conn := <-c:
		return conn, nil
	default:
		return nil, errors.New("nobody answers")
	}
}

// attachTo attaches to a service over conn, saying mine, redialling back.
func attachTo(t *testing.T, conn net.Conn, mine wire.Hello, back comingBack) *dial.Attachment {
	t.Helper()
	a, err := dial.Dial(context.Background(), conn, mine, dial.Redial(back.redial))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// until reads a's stream until it carries a value like want, failing the test
// if the stream ends or nothing comes.
func until[T any](t *testing.T, a *dial.Attachment) T {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case v, ok := <-a.Stream():
			require.True(t, ok, "the stream ended")
			if got, is := v.(T); is {
				return got
			}
		case <-deadline:
			var zero T
			require.FailNow(t, "nothing came", "%T", zero)
			return zero
		}
	}
}

// streamEnds waits for a's stream to end, failing the test if it does not.
func streamEnds(t *testing.T, a *dial.Attachment) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-a.Stream():
			if !ok {
				return
			}
		case <-deadline:
			require.FailNow(t, "the stream did not end")
		}
	}
}

// A client of a service that went waits for it, and once it is back, makes
// again the subscriptions it holds, under the ids it chose, and says again
// what it is showing (#210).
func TestReattach_AServiceComesBackWithTheSubscriptionsAndTheFocus(t *testing.T) {
	dial.ReattachFor(t, 20*time.Millisecond, 3*time.Second)
	first := newRecorder()
	conn, drop := hostOn(t, first, service("2.0.0"))
	back := make(comingBack, 1)
	a := attachTo(t, conn, service("2.0.0"), back)

	gone := a.Subscribe(project.AccountWindow{})
	kept := a.Subscribe(project.ChatListWindow{Limit: 10})
	a.Unsubscribe(gone)
	a.MoveWindow(kept, project.ChatListWindow{Limit: 20})
	a.SetFocus(domain.HistoryKey{ChatID: 5})
	for range 5 {
		first.next(t)
	}

	second := newRecorder()
	again, _ := hostOn(t, second, service("2.0.0"))
	back <- again
	drop()

	until[dial.Reconnecting](t, a)
	until[dial.Reconnected](t, a)
	assert.Equal(t, received{method: "SubscribeAs", args: []any{kept, project.ChatListWindow{Limit: 20}}}, second.next(t))
	assert.Equal(t, received{method: "SetFocus", args: []any{domain.HistoryKey{ChatID: 5}}}, second.next(t))
	assert.Greater(t, a.Subscribe(project.AccountWindow{}), kept, "ids go on growing")
}

// Only a service is started again, so only a client of one waits: any other
// host that goes is gone (#210).
func TestReattach_OnlyAServiceIsWaitedFor(t *testing.T) {
	dial.ReattachFor(t, 20*time.Millisecond, 3*time.Second)
	window := hello
	window.Kept = wire.KeptByWindow
	conn, drop := hostOn(t, newRecorder(), window)
	back := make(comingBack, 1)
	a := attachTo(t, conn, hello, back)
	again, _ := hostOn(t, newRecorder(), window)
	back <- again

	drop()

	deadline := time.After(3 * time.Second)
	for ended := false; !ended; {
		select {
		case v, ok := <-a.Stream():
			ended = !ok
			_, waiting := v.(dial.Reconnecting)
			require.False(t, waiting, "a client of a window waited for it")
		case <-deadline:
			require.FailNow(t, "the stream did not end")
		}
	}
	lost, ok := telerr.As(a.Lost())
	require.True(t, ok, "got %v", a.Lost())
	assert.Equal(t, telerr.Network, lost.Kind)
}

// A service that does not come back in time is given up on: something is
// wrong beyond a restart (#210).
func TestReattach_GivesUpOnAServiceThatDoesNotComeBack(t *testing.T) {
	dial.ReattachFor(t, 10*time.Millisecond, 150*time.Millisecond)
	conn, drop := hostOn(t, newRecorder(), service("2.0.0"))
	a := attachTo(t, conn, service("2.0.0"), make(comingBack))

	drop()

	until[dial.Reconnecting](t, a)
	streamEnds(t, a)
	assert.ErrorIs(t, a.Lost(), dial.ErrGaveUp)
}

// A service that comes back newer than this client turns it away: tele was
// updated (#183).
func TestReattach_AServiceBackNewerTurnsTheClientAway(t *testing.T) {
	dial.ReattachFor(t, 20*time.Millisecond, 3*time.Second)
	conn, drop := hostOn(t, newRecorder(), service("2.0.0"))
	back := make(comingBack, 1)
	mine := hello
	mine.Binary = "2.0.0"
	a := attachTo(t, conn, mine, back)
	again, _ := hostOn(t, newRecorder(), service("2.1.0"))
	back <- again

	drop()

	streamEnds(t, a)
	var mismatch *dial.Mismatch
	require.ErrorAs(t, a.Lost(), &mismatch)
	assert.Equal(t, wire.RestartClient, mismatch.Verdict)
}

// While the host is away a call fails at once, as a network failure that may
// pass, rather than waiting for a host that may never answer it.
func TestReattach_ACallWhileTheHostIsAwayFailsAtOnce(t *testing.T) {
	dial.ReattachFor(t, 20*time.Millisecond, 3*time.Second)
	conn, drop := hostOn(t, newRecorder(), service("2.0.0"))
	a := attachTo(t, conn, service("2.0.0"), make(comingBack))
	drop()
	until[dial.Reconnecting](t, a)

	done := make(chan error, 1)
	go func() { done <- a.RetryOutbox("ref") }()

	select {
	case err := <-done:
		lost, ok := telerr.As(err)
		require.True(t, ok, "got %v", err)
		assert.True(t, lost.Transient)
	case <-time.After(time.Second):
		t.Fatal("a call waited for a host that is away")
	}
}

// A client that closes while it waits stops waiting.
func TestReattach_ClosingStopsTheWait(t *testing.T) {
	dial.ReattachFor(t, 20*time.Millisecond, 3*time.Second)
	conn, drop := hostOn(t, newRecorder(), service("2.0.0"))
	a := attachTo(t, conn, service("2.0.0"), make(comingBack))
	drop()
	until[dial.Reconnecting](t, a)

	_ = a.Close()

	streamEnds(t, a)
}
