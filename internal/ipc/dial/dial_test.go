package dial_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/ipctest"
	"github.com/sorokin-vladimir/tele/internal/ipc/serve"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// hello is what both ends say of themselves when they are the same build.
var hello = wire.Hello{Protocol: wire.ProtocolVersion, Binary: "dev", Config: "/tmp/tele/config.yml"}

// connect is an attachment over a connection whose other end serves s.
func connect(t *testing.T, s serve.Attachment) *dial.Attachment {
	t.Helper()
	client, host := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = serve.Conn(ctx, host, s, &hostEnd{hello: hello})
	}()
	a, err := dial.Dial(ctx, client, hello)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = a.Close()
		cancel()
		<-served
	})
	return a
}

// The subscriptions take a window, which no filler can choose: they have tests
// of their own.
var notYet = map[string]bool{"Subscribe": true, "MoveWindow": true, "Unsubscribe": true}

// Every call a client may make reaches the surface on the other side with the
// arguments it was made with, and the caller gets back what the surface
// answered, less its addresses. The methods are taken from the surface itself,
// so a method added to it is covered the day it is added.
func TestEveryCallCrosses(t *testing.T) {
	surface := reflect.TypeFor[core.Surface]()
	ctxType := reflect.TypeFor[context.Context]()
	errType := reflect.TypeFor[error]()
	for i := range surface.NumMethod() {
		m := surface.Method(i)
		if notYet[m.Name] {
			continue
		}
		t.Run(m.Name, func(t *testing.T) {
			rec := newRecorder()
			var want []any
			var results ipctest.Filler
			for j := range m.Type.NumOut() {
				if out := m.Type.Out(j); out != errType {
					v := reflect.New(out).Elem()
					results.Fill(v)
					want = append(want, v.Interface())
				}
			}
			rec.results[m.Name] = want
			a := connect(t, rec)

			var in []reflect.Value
			var sent []any
			var filler ipctest.Filler
			for j := range m.Type.NumIn() {
				p := m.Type.In(j)
				if p == ctxType {
					in = append(in, reflect.ValueOf(context.Background()))
					continue
				}
				v := reflect.New(p).Elem()
				filler.Fill(v)
				in = append(in, v)
				sent = append(sent, v.Interface())
			}
			outs := reflect.ValueOf(a).MethodByName(m.Name).Call(in)

			got := rec.next(t)
			assert.Equal(t, m.Name, got.method)
			assert.Equal(t, sent, got.args)

			var answered []any
			for _, out := range outs {
				if out.Type() == errType {
					assert.True(t, out.IsNil(), "%v", out.Interface())
					continue
				}
				answered = append(answered, out.Interface())
			}
			var stripped []any
			for _, w := range want {
				stripped = append(stripped, withoutAddresses(t, w))
			}
			assert.Equal(t, stripped, answered)
		})
	}
}

// A refusal arrives as the domain error the surface gave, kind and all.
func TestADomainErrorCrossesWhole(t *testing.T) {
	rec := newRecorder()
	refusal := &telerr.Error{Kind: telerr.RateLimited, Op: "messages.setTyping", RetryAfter: 3 * time.Second}
	rec.errs["SetMuted"] = refusal
	a := connect(t, rec)

	err := a.SetMuted(context.Background(), domain.HistoryKey{ChatID: 1}, true)

	assert.Equal(t, refusal, err)
}

// An error with no kind has only its text to say, and says it as an Internal
// one, the way an error leaves the owner as a value in-process too.
func TestAnErrorWithoutAKindCrossesAsItsText(t *testing.T) {
	rec := newRecorder()
	rec.errs["RetryOutbox"] = errors.New("disk full")
	a := connect(t, rec)

	err := a.RetryOutbox("r1")

	assert.Equal(t, &telerr.Error{Kind: telerr.Internal, Detail: "disk full"}, err)
}

// withoutAddresses is v as a client receives it: what the owner keeps to reach
// a chat or a file is zero, everything else as it was.
func withoutAddresses(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	p := reflect.New(reflect.TypeOf(v))
	require.NoError(t, json.Unmarshal(raw, p.Interface()))
	zeroAddresses(p.Elem())
	return p.Elem().Interface()
}

func zeroAddresses(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			zeroAddresses(v.Elem())
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := range v.Len() {
			zeroAddresses(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(v.MapIndex(k))
			zeroAddresses(e)
			v.SetMapIndex(k, e)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			f := v.Field(i)
			if !f.CanSet() {
				continue
			}
			switch v.Type().Field(i).Name {
			case "AccessHash", "FileReference":
				f.SetZero()
			default:
				zeroAddresses(f)
			}
		}
	}
}
