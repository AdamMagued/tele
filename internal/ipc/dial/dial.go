// Package dial is the client's end of an attachment over a connection: the
// surface a client holds, made of calls to a host on the other side. A client
// cannot tell it from an attachment in its own process (#209).
package dial

import (
	"context"
	"errors"
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
	// nextID is the last request id used. Ids only grow.
	nextID atomic.Uint64

	mu sync.Mutex
	// waiting holds the calls whose answer is still to come, by request id.
	waiting map[uint64]chan wire.Frame
	// lost is closed when the connection ends, and lostErr says why.
	lost    chan struct{}
	lostErr error
}

var _ core.Surface = (*Attachment)(nil)

// New is an attachment over conn, which it owns from here.
func New(conn io.ReadWriteCloser) *Attachment {
	a := &Attachment{
		conn:    conn,
		enc:     wire.NewEncoder(conn),
		waiting: map[uint64]chan wire.Frame{},
		lost:    make(chan struct{}),
	}
	go a.read(wire.NewDecoder(conn))
	return a
}

// Close ends the attachment: the client is gone.
func (a *Attachment) Close() error { return a.conn.Close() }

// hostLost is what a call answers when the connection ended: the host may be
// back, and the account it served may be too.
func hostLost() error {
	return &telerr.Error{Kind: telerr.Network, Op: "host", Transient: true}
}

// read hands every answer to the call waiting for it, until the connection
// ends.
func (a *Attachment) read(dec *wire.Decoder) {
	for {
		f, err := dec.Decode()
		if err != nil {
			a.mu.Lock()
			a.lostErr = hostLost()
			close(a.lost)
			a.mu.Unlock()
			return
		}
		shape, err := f.Shape()
		if err != nil {
			continue
		}
		switch shape {
		case wire.ShapeReply, wire.ShapeError:
			a.mu.Lock()
			ch, ok := a.waiting[f.ID]
			delete(a.waiting, f.ID)
			a.mu.Unlock()
			if ok {
				ch <- f
			}
		}
	}
}

// call makes method with args and waits for its answer, which it decodes into
// dsts. A call whose ctx ends first answers with that, and its answer is
// dropped when it comes.
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

	req, err := wire.Request(id, method, params)
	if err != nil {
		return err
	}
	if err := a.enc.Encode(req); err != nil {
		return hostLost()
	}
	select {
	case f := <-answer:
		if f.Error != nil {
			return f.Error
		}
		return c.Decode(f.Result, dsts...)
	case <-ctx.Done():
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
	req, err := wire.Request(a.nextID.Add(1), method, params)
	if err != nil {
		return
	}
	_ = a.enc.Encode(req)
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

// errNotYet is a call this end cannot make yet.
var errNotYet = errors.New("dial: subscriptions do not cross yet")
