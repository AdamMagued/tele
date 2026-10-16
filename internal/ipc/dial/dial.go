// Package dial is the client's end of an attachment over a connection: the
// surface a client holds, made of calls to a host on the other side, and the
// stream the host tells it on. A client cannot tell it from an attachment in
// its own process (#209).
package dial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/ipc/protocol"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// Attachment is an attachment over a connection.
type Attachment struct {
	conn io.ReadWriteCloser
	enc  *wire.Encoder
	// nextID is the last request id used, nextSub the last subscription id.
	// Both only grow, and both are this side's to choose.
	nextID  atomic.Uint64
	nextSub atomic.Uint64

	// stream carries deltas and events to the client, in the order the host
	// sent them. It holds as much as the host's side of an attachment does.
	stream chan any
	// closed is closed when the client closes the attachment.
	closed    chan struct{}
	closeOnce sync.Once

	mu sync.Mutex
	// waiting holds the calls whose answer is still to come, by request id.
	waiting map[uint64]chan wire.Frame
	// lost is closed when the connection ends, and lostErr says why.
	lost    chan struct{}
	lostErr error
}

var _ core.Surface = (*Attachment)(nil)

// Mismatch is a host this client does not use as it is: what to do about it,
// and what the host said of itself, such as the config file it reads.
type Mismatch struct {
	Verdict wire.Verdict
	Host    wire.Hello
}

func (m *Mismatch) Error() string {
	return fmt.Sprintf("the host runs %s on protocol %d, and this client cannot use it as it is (verdict %d)",
		m.Host.Binary, m.Host.Protocol, m.Verdict)
}

// Dial attaches over conn, which it owns from here: it says hello, hears the
// host's, and decides whether the two understand each other. A host it does
// not use answers with a *Mismatch, and conn is closed.
func Dial(ctx context.Context, conn io.ReadWriteCloser, hello wire.Hello) (*Attachment, error) {
	// Hello is said before anything reads the connection, so its answer is
	// read here; a ctx that ends first closes conn, which ends the wait.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	host, dec, err := greet(conn, hello)
	if !stop() || err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if v := wire.Decide(hello, host); v != wire.Proceed {
		_ = conn.Close()
		return nil, &Mismatch{Verdict: v, Host: host}
	}
	a := &Attachment{
		conn:    conn,
		enc:     wire.NewEncoder(conn),
		stream:  make(chan any, core.StreamSize),
		closed:  make(chan struct{}),
		waiting: map[uint64]chan wire.Frame{},
		lost:    make(chan struct{}),
	}
	a.nextID.Store(helloID)
	go a.read(dec)
	return a, nil
}

// helloID is the request id hello goes under: the first one.
const helloID = 1

func greet(conn io.ReadWriter, hello wire.Hello) (wire.Hello, *wire.Decoder, error) {
	var host wire.Hello
	raw, err := json.Marshal(hello)
	if err != nil {
		return host, nil, err
	}
	if err := wire.NewEncoder(conn).Encode(wire.Request(helloID, wire.MethodHello, raw)); err != nil {
		return host, nil, hostLost()
	}
	dec := wire.NewDecoder(conn)
	f, err := dec.Decode()
	if err != nil {
		if errors.Is(err, wire.ErrProtocol) {
			return host, nil, protocol.Violation(err)
		}
		return host, nil, hostLost()
	}
	if f.Error != nil {
		return host, nil, f.Error
	}
	if f.ID != helloID || f.Result == nil {
		return host, nil, protocol.Violation(fmt.Errorf("%w: the host did not answer hello", wire.ErrProtocol))
	}
	if err := json.Unmarshal(f.Result, &host); err != nil {
		return host, nil, protocol.Violation(fmt.Errorf("%w: hello: %v", wire.ErrProtocol, err))
	}
	return host, dec, nil
}

// Stream is everything the host tells this client, deltas and events alike,
// in the order it told them: the same values an attachment in the host's own
// process carries.
func (a *Attachment) Stream() <-chan any { return a.stream }

// Close ends the attachment: the client is gone.
func (a *Attachment) Close() error {
	a.closeOnce.Do(func() { close(a.closed) })
	return a.conn.Close()
}

// hostLost is what a call answers when the connection ended: the host may be
// back, and the account it served may be too.
func hostLost() *telerr.Error {
	return &telerr.Error{Kind: telerr.Network, Op: "host", Transient: true}
}

// read takes the host's frames off the connection until it ends: answers go
// to the calls waiting for them, deltas and events onto the stream. A client
// that is not draining its stream holds this up, and the host with it, as it
// would hold up an attachment in the host's own process.
//
// When it ends, every call waiting and every call after is answered with why,
// and the stream ends, which is how its holder hears of it.
func (a *Attachment) read(dec *wire.Decoder) {
	why, broke := a.readUntilEnd(dec)
	a.mu.Lock()
	a.lostErr = why
	close(a.lost)
	a.mu.Unlock()
	close(a.stream)
	if broke {
		_ = a.enc.Encode(wire.Fail(0, why))
	}
	_ = a.conn.Close()
}

// readUntilEnd reads until the connection ends, and says why it did. broke
// says the host broke the protocol, which it is told before the connection
// goes.
func (a *Attachment) readUntilEnd(dec *wire.Decoder) (why *telerr.Error, broke bool) {
	for {
		f, err := dec.Decode()
		if err != nil {
			if errors.Is(err, wire.ErrProtocol) {
				return protocol.Violation(err), true
			}
			return hostLost(), false
		}
		shape, err := f.Shape()
		if err != nil {
			return protocol.Violation(err), true
		}
		switch shape {
		case wire.ShapeError:
			if f.ID == 0 {
				// The host says why it is going: that is the reason, not
				// the connection ending after it.
				return f.Error, false
			}
			fallthrough
		case wire.ShapeReply:
			a.mu.Lock()
			ch, ok := a.waiting[f.ID]
			delete(a.waiting, f.ID)
			a.mu.Unlock()
			if ok {
				ch <- f
			}
		case wire.ShapeDelta, wire.ShapeEvent:
			v, err := protocol.Unframe(f)
			if err != nil {
				return protocol.Violation(err), true
			}
			select {
			case a.stream <- v:
			case <-a.closed:
				return hostLost(), false
			}
		default:
			return protocol.Violation(fmt.Errorf("%w: a host does not send %v", wire.ErrProtocol, shape)), true
		}
	}
}

// call makes method with args and waits for its answer, which it decodes into
// dsts. A call whose ctx ends first is called off on the host and answers with
// ctx's error; its answer, if one comes, is dropped.
func (a *Attachment) call(ctx context.Context, method string, args []any, dsts ...any) error {
	c := protocol.Calls[method]
	params, err := c.Params(args...)
	if err != nil {
		return err
	}
	id := a.nextID.Add(1)
	answer := make(chan wire.Frame, 1)
	a.mu.Lock()
	select {
	case <-a.lost:
		a.mu.Unlock()
		return a.lostErr
	default:
	}
	a.waiting[id] = answer
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.waiting, id)
		a.mu.Unlock()
	}()

	if err := a.enc.Encode(wire.Request(id, method, params)); err != nil {
		return hostLost()
	}
	select {
	case f := <-answer:
		if f.Error != nil {
			return f.Error
		}
		return c.Decode(f.Result, dsts...)
	case <-ctx.Done():
		_ = a.enc.Encode(wire.Cancel(id))
		return ctx.Err()
	case <-a.lost:
		return a.lostErr
	}
}

// tell makes method with args and does not wait: it has nothing to answer.
func (a *Attachment) tell(method string, args ...any) {
	params, err := protocol.Calls[method].Params(args...)
	if err != nil {
		return
	}
	_ = a.enc.Encode(wire.Request(a.nextID.Add(1), method, params))
}

func do(a *Attachment, ctx context.Context, method string, args ...any) error {
	return a.call(ctx, method, args)
}

func ask[T any](a *Attachment, ctx context.Context, method string, args ...any) (T, error) {
	var v T
	err := a.call(ctx, method, args, &v)
	return v, err
}

func ask2[T, U any](a *Attachment, ctx context.Context, method string, args ...any) (T, U, error) {
	var v T
	var u U
	err := a.call(ctx, method, args, &v, &u)
	return v, u, err
}
