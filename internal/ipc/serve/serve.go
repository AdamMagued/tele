// Package serve is the host's end of an attachment over a connection: it reads
// a client's calls off the connection, makes them on the surface the host
// gives it, and writes back what they answered (#209).
package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/ipc/protocol"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// Conn serves s over conn until the client goes, ctx ends, or the client says
// something that is not the protocol. It owns conn from here and closes it.
//
// A call that takes a context runs beside the others; one that does not runs
// on the reading goroutine, in the order it was asked.
func Conn(ctx context.Context, conn io.ReadWriteCloser, s core.Surface) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()

	var running sync.WaitGroup
	defer running.Wait()

	enc := wire.NewEncoder(conn)
	dec := wire.NewDecoder(conn)
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
			c, ok := protocol.Calls[f.Method]
			if !ok {
				_ = enc.Encode(wire.Fail(f.ID, &telerr.Error{
					Kind: telerr.Internal, Op: f.Method, Detail: "the host has no such call",
				}))
				continue
			}
			args, err := c.Args(f.Params)
			if err != nil {
				return err
			}
			if !c.Context {
				answer(ctx, enc, s, c, f.ID, args)
				continue
			}
			running.Go(func() { answer(ctx, enc, s, c, f.ID, args) })
		case wire.ShapeCancel:
			// Calling off arrives with the stream (#209).
		default:
			return fmt.Errorf("%w: a client does not send %v", wire.ErrProtocol, shape)
		}
	}
}

// answer makes call c with args and writes back what it answered.
func answer(ctx context.Context, enc *wire.Encoder, s core.Surface, c protocol.Call, id uint64, args []reflect.Value) {
	outs, err := c.Invoke(ctx, s, args)
	if err != nil {
		_ = enc.Encode(wire.Fail(id, err))
		return
	}
	result, err := c.Result(outs)
	if err != nil {
		_ = enc.Encode(wire.Fail(id, err))
		return
	}
	reply, err := wire.Reply(id, result)
	if err != nil {
		_ = enc.Encode(wire.Fail(id, err))
		return
	}
	_ = enc.Encode(reply)
}
