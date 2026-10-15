// Package serve is the host's end of an attachment over a connection: it reads
// a client's calls off the connection, makes them on the attachment the host
// gives it, writes back what they answered, and passes on everything the
// attachment's stream carries (#209).
package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/ipc/protocol"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// Attachment is what the host serves a connection from: the surface a client
// may call, the stream it is told on, the subscriptions under the ids the
// client chose, and the end of it all when the client goes.
type Attachment interface {
	core.Surface
	SubscribeAs(id project.SubID, w project.Window) error
	Stream() <-chan any
	Detach()
}

var _ Attachment = (*core.Attachment)(nil)

// Conn serves att over conn until the client goes, ctx ends, or the client
// says something that is not the protocol. It owns conn from here and closes
// it, and detaches att when it returns: the client is gone.
//
// A call that takes a context runs beside the others and can be called off; one
// that does not runs on the reading goroutine, in the order it was asked.
func Conn(ctx context.Context, conn io.ReadWriteCloser, att Attachment) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Ending ctx closes conn, which is what frees a goroutine blocked on it.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	c := &served{att: att, enc: wire.NewEncoder(conn), calls: map[uint64]context.CancelFunc{}}
	// running is the calls still being made. Only the reading goroutine adds
	// to it, so once that has returned it can be waited on.
	var running sync.WaitGroup
	pumped := make(chan error, 1)
	go func() { pumped <- c.pump(ctx) }()
	read := make(chan error, 1)
	go func() { read <- c.read(ctx, wire.NewDecoder(conn), &running) }()

	var err error
	select {
	case err = <-read:
		cancel()
		<-pumped
	case err = <-pumped:
		cancel()
		<-read
	}
	_ = conn.Close()
	running.Wait()
	att.Detach()
	return err
}

// served is one connection being served.
type served struct {
	att Attachment
	enc *wire.Encoder

	mu sync.Mutex
	// calls holds what calls off each call still running, by request id.
	calls map[uint64]context.CancelFunc
}

// pump passes on everything the attachment's stream carries, until ctx ends.
func (c *served) pump(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case v := <-c.att.Stream():
			f, err := protocol.Frame(v)
			if err != nil {
				return &telerr.Error{Kind: telerr.Internal, Op: "stream", Detail: err.Error(), Cause: err}
			}
			if err := c.enc.Encode(f); err != nil {
				return nil
			}
		}
	}
}

// read takes the client's frames off the connection until it ends.
func (c *served) read(ctx context.Context, dec *wire.Decoder, running *sync.WaitGroup) error {
	for {
		f, err := dec.Decode()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		shape, err := f.Shape()
		if err != nil {
			return err
		}
		switch shape {
		case wire.ShapeRequest:
			if err := c.request(ctx, f, running); err != nil {
				return err
			}
		case wire.ShapeCancel:
			id, err := f.CancelledID()
			if err != nil {
				return err
			}
			// A call that already answered is no longer here: the cancel
			// crossed its reply, which is no fault of either side.
			c.mu.Lock()
			if stop, ok := c.calls[id]; ok {
				stop()
			}
			c.mu.Unlock()
		default:
			return fmt.Errorf("%w: a client does not send %v", wire.ErrProtocol, shape)
		}
	}
}

// request makes the call f asks for.
func (c *served) request(ctx context.Context, f wire.Frame, running *sync.WaitGroup) error {
	call, ok := protocol.Calls[f.Method]
	if !ok {
		_ = c.enc.Encode(wire.Fail(f.ID, &telerr.Error{
			Kind: telerr.Internal, Op: f.Method, Detail: "the host has no such call",
		}))
		return nil
	}
	args, err := call.Args(f.Params)
	if err != nil {
		return err
	}
	if f.Method == protocol.MethodSubscribe {
		id, w := args[0].Interface().(project.SubID), args[1].Interface().(project.Window)
		if err := c.att.SubscribeAs(id, w); err != nil {
			return fmt.Errorf("%w: %v", wire.ErrProtocol, err)
		}
		return c.reply(f.ID, call, nil)
	}
	if !call.Context {
		outs, err := call.Invoke(ctx, c.att, args)
		if err != nil {
			return c.fail(f.ID, err)
		}
		return c.reply(f.ID, call, outs)
	}
	callCtx, stop := context.WithCancel(ctx)
	c.mu.Lock()
	c.calls[f.ID] = stop
	c.mu.Unlock()
	running.Go(func() {
		defer func() {
			c.mu.Lock()
			delete(c.calls, f.ID)
			c.mu.Unlock()
			stop()
		}()
		outs, err := call.Invoke(callCtx, c.att, args)
		if err != nil {
			_ = c.fail(f.ID, err)
			return
		}
		_ = c.reply(f.ID, call, outs)
	})
	return nil
}

// reply answers request id with what call answered. A reply that cannot be
// written means the client is gone, which the reading side finds out by
// itself.
func (c *served) reply(id uint64, call protocol.Call, outs []reflect.Value) error {
	result, err := call.Result(outs)
	if err != nil {
		return c.fail(id, err)
	}
	f, err := wire.Reply(id, result)
	if err != nil {
		return c.fail(id, err)
	}
	_ = c.enc.Encode(f)
	return nil
}

func (c *served) fail(id uint64, err error) error {
	_ = c.enc.Encode(wire.Fail(id, err))
	return nil
}
