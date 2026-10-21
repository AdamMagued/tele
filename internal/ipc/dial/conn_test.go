package dial_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ipc"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/serve"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// Over a real socket, the way a client reaches its host: the handshake, a
// call and what the host tells all cross.
func TestAClientReachesItsHostOverTheSocket(t *testing.T) {
	ep, err := ipc.EndpointFor("tele-test", t.TempDir())
	require.NoError(t, err)
	l, err := ipc.Listen(ep)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	att := core.NewAttachment()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		_ = serve.Conn(ctx, conn, att, &hostEnd{hello: hello})
	}()
	t.Cleanup(func() {
		cancel()
		<-served
	})

	conn, err := ipc.Dial(ctx, ep)
	require.NoError(t, err)
	a, err := dial.Dial(ctx, conn, hello)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	// No owner is seated, so the call is answered by the attachment itself.
	err = a.SetMuted(ctx, domain.HistoryKey{ChatID: 1}, true)
	assert.Equal(t, telerr.AccountEnded, telerr.Of(err))

	att.Tell(core.Incoming{ChatID: 5})
	select {
	case got := <-a.Stream():
		assert.Equal(t, core.Incoming{ChatID: 5}, got)
	case <-time.After(2 * time.Second):
		t.Fatal("the event never arrived")
	}
}

// A client keeps what its host said of itself in hello, such as the config
// file the host reads, whose settings the client then leaves to it (#208).
func TestAClientKnowsWhatItsHostSaidOfItself(t *testing.T) {
	ep, err := ipc.EndpointFor("tele-test", t.TempDir())
	require.NoError(t, err)
	l, err := ipc.Listen(ep)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	host := hello
	host.Config = "/home/ada/.config/tele/host.yml"
	go func() {
		conn, err := l.Accept()
		if err == nil {
			_ = serve.Conn(context.Background(), conn, core.NewAttachment(), &hostEnd{hello: host})
		}
	}()
	conn, err := ipc.Dial(context.Background(), ep)
	require.NoError(t, err)

	a, err := dial.Dial(context.Background(), conn, hello)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	assert.Equal(t, host, a.Host())
}

// A host that stops says so, and that is the last thing its client hears: the
// connection ends on it, without the host's context being called off, so the
// client can tell a host that quit from one that broke (#208).
func TestAHostThatStopsSaysSoLast(t *testing.T) {
	ep, err := ipc.EndpointFor("tele-test", t.TempDir())
	require.NoError(t, err)
	l, err := ipc.Listen(ep)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	att := core.NewAttachment()
	served := make(chan error, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			served <- err
			return
		}
		served <- serve.Conn(context.Background(), conn, att, &hostEnd{hello: hello})
	}()
	conn, err := ipc.Dial(context.Background(), ep)
	require.NoError(t, err)
	a, err := dial.Dial(context.Background(), conn, hello)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	att.Tell(core.HostStopping{Reason: core.StopRestart})

	select {
	case got := <-a.Stream():
		assert.Equal(t, core.HostStopping{Reason: core.StopRestart}, got, "why the host stops crosses")
	case <-time.After(2 * time.Second):
		t.Fatal("the client never heard the host was stopping")
	}
	select {
	case _, open := <-a.Stream():
		assert.False(t, open, "nothing follows it")
	case <-time.After(2 * time.Second):
		t.Fatal("the connection outlived the host's stop")
	}
	select {
	case err := <-served:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("serving the connection did not end")
	}
}

// fakeHost is a host the test plays by hand: it answers the handshake, and
// the rest of the connection is the test's to drive.
type fakeHost struct {
	conn net.Conn
	dec  *wire.Decoder
	enc  *wire.Encoder
}

// attachToFake is a client attached to a host the test plays.
func attachToFake(t *testing.T) (*dial.Attachment, fakeHost) {
	t.Helper()
	client, conn := net.Pipe()
	t.Cleanup(func() { _ = conn.Close() })
	h := fakeHost{conn: conn, dec: wire.NewDecoder(conn), enc: wire.NewEncoder(conn)}
	go func() {
		f, err := h.dec.Decode()
		if err != nil {
			return
		}
		raw, _ := json.Marshal(hello)
		_ = h.enc.Encode(wire.Reply(f.ID, raw))
	}()
	a, err := dial.Dial(context.Background(), client, hello)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	return a, h
}

// waitFor is the next request the fake host receives.
func (h fakeHost) waitFor(t *testing.T) wire.Frame {
	t.Helper()
	f, err := h.dec.Decode()
	require.NoError(t, err)
	return f
}

// A host that goes away answers every call in flight, and every call after,
// with a failure a retry may get past, and the stream ends.
func TestAHostThatGoesAwayEndsEverything(t *testing.T) {
	a, h := attachToFake(t)
	done := make(chan error, 1)
	go func() {
		_, err := a.Chats(context.Background())
		done <- err
	}()
	h.waitFor(t)

	require.NoError(t, h.conn.Close())

	lost := &telerr.Error{Kind: telerr.Network, Op: "host", Transient: true}
	select {
	case err := <-done:
		assert.Equal(t, lost, err)
	case <-time.After(2 * time.Second):
		t.Fatal("the call in flight was never answered")
	}
	assert.Equal(t, lost, a.SetMuted(context.Background(), domain.HistoryKey{ChatID: 1}, true))
	select {
	case _, open := <-a.Stream():
		assert.False(t, open, "the stream ends")
	case <-time.After(2 * time.Second):
		t.Fatal("the stream never ended")
	}
}

// A host that says something that is not the protocol is not a host that went
// away: the calls in flight say so, rather than inviting a retry.
func TestAHostOffTheProtocolSaysSo(t *testing.T) {
	a, h := attachToFake(t)
	done := make(chan error, 1)
	go func() {
		_, err := a.Chats(context.Background())
		done <- err
	}()
	h.waitFor(t)

	_, err := h.conn.Write([]byte("this is no frame\n"))
	require.NoError(t, err)

	select {
	case err := <-done:
		e, ok := telerr.As(err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, telerr.Internal, e.Kind)
		assert.Equal(t, "protocol", e.Op)
	case <-time.After(2 * time.Second):
		t.Fatal("the call in flight was never answered")
	}
	// The client tells the host why it went, before it goes.
	f, err := h.dec.Decode()
	require.NoError(t, err)
	require.NotNil(t, f.Error)
	assert.Equal(t, uint64(0), f.ID)
	assert.Equal(t, "protocol", f.Error.Op)
}

// A client never uses a host it does not understand: it is told which host it
// reached and what to do about it, and the connection is given up.
func TestAClientRefusesAHostOnAnotherProtocol(t *testing.T) {
	client, conn := net.Pipe()
	t.Cleanup(func() { _ = conn.Close() })
	other := wire.Hello{Protocol: wire.ProtocolVersion + 1, Binary: "dev", Config: "/elsewhere.yml"}
	go func() {
		dec, enc := wire.NewDecoder(conn), wire.NewEncoder(conn)
		f, err := dec.Decode()
		if err != nil {
			return
		}
		raw, _ := json.Marshal(other)
		_ = enc.Encode(wire.Reply(f.ID, raw))
		_, _ = dec.Decode() // until the client gives the connection up
	}()

	_, err := dial.Dial(context.Background(), client, hello)

	var m *dial.Mismatch
	require.ErrorAs(t, err, &m)
	assert.Equal(t, wire.Refuse, m.Verdict)
	assert.Equal(t, other, m.Host)
	_, err = client.Write([]byte("x"))
	assert.Error(t, err, "the connection is given up")
}
