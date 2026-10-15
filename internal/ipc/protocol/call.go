package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

// Call is one method of the surface as it crosses a connection. Its params are
// the method's arguments as a JSON array, in order and without the context,
// which does not cross. Its result is what the method answers besides an
// error: nothing, one value, or an array of them when there are several. The
// error travels as an error frame instead (#209).
type Call struct {
	Name string
	// Context says the method takes one: it may wait on Telegram, so it runs
	// beside the others and can be called off. A method without one is quick
	// and runs in the order it was asked, which is what keeps a window moved
	// after it was opened, and a focus set after the last one.
	Context bool
	// Answers says the caller waits for a reply: the method has a result or
	// an error to give. One that has neither is told, and its reply dropped.
	Answers bool

	method reflect.Method
	in     []reflect.Type
	out    []reflect.Type
}

// Calls is every method of the surface, by name.
var Calls = calls()

func calls() map[string]Call {
	ctxType := reflect.TypeFor[context.Context]()
	errType := reflect.TypeFor[error]()
	surface := reflect.TypeFor[core.Surface]()
	all := make(map[string]Call, surface.NumMethod())
	for i := range surface.NumMethod() {
		m := surface.Method(i)
		c := Call{Name: m.Name, method: m}
		for j := range m.Type.NumIn() {
			p := m.Type.In(j)
			if j == 0 && p == ctxType {
				c.Context = true
				continue
			}
			mustCross(m.Name, p)
			c.in = append(c.in, p)
		}
		for j := range m.Type.NumOut() {
			o := m.Type.Out(j)
			c.Answers = true
			if j == m.Type.NumOut()-1 && o == errType {
				continue
			}
			mustCross(m.Name, o)
			c.out = append(c.out, o)
		}
		all[m.Name] = c
	}
	return all
}

// mustCross refuses a type no connection can carry. The surface is put
// together once, so one that has such a method is a bug, and panics.
func mustCross(method string, t reflect.Type) {
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		panic(fmt.Sprintf("protocol: %s takes or answers %v, which cannot cross a connection", method, t))
	}
}

// Params encodes args, the arguments a call is made with, without its context.
func (c Call) Params(args ...any) (json.RawMessage, error) {
	if len(args) != len(c.in) {
		return nil, fmt.Errorf("protocol: %s takes %d arguments, not %d", c.Name, len(c.in), len(args))
	}
	if args == nil {
		args = []any{}
	}
	return json.Marshal(args)
}

// Args decodes params into the arguments the method is called with, without
// its context. Params the method cannot take are the other side speaking
// another protocol.
func (c Call) Args(params json.RawMessage) ([]reflect.Value, error) {
	var raws []json.RawMessage
	if len(params) > 0 {
		if err := json.Unmarshal(params, &raws); err != nil {
			return nil, fmt.Errorf("%w: %s params: %v", wire.ErrProtocol, c.Name, err)
		}
	}
	if len(raws) != len(c.in) {
		return nil, fmt.Errorf("%w: %s takes %d arguments, not %d", wire.ErrProtocol, c.Name, len(c.in), len(raws))
	}
	args := make([]reflect.Value, len(c.in))
	for i, t := range c.in {
		p := reflect.New(t)
		if err := strictUnmarshal(raws[i], p.Interface()); err != nil {
			return nil, fmt.Errorf("%w: %s argument %d: %v", wire.ErrProtocol, c.Name, i, err)
		}
		args[i] = p.Elem()
	}
	return args, nil
}

// Invoke calls the method on s with args under ctx, and gives what it
// answered besides an error, and the error.
func (c Call) Invoke(ctx context.Context, s core.Surface, args []reflect.Value) ([]reflect.Value, error) {
	in := args
	if c.Context {
		in = append([]reflect.Value{reflect.ValueOf(ctx)}, args...)
	}
	outs := reflect.ValueOf(s).Method(c.method.Index).Call(in)
	if len(outs) > len(c.out) {
		last := outs[len(outs)-1]
		outs = outs[:len(outs)-1]
		if !last.IsNil() {
			return outs, last.Interface().(error)
		}
	}
	return outs, nil
}

// Result encodes what a method answered besides an error, less its addresses:
// this is the one place a result leaves the host.
func (c Call) Result(outs []reflect.Value) (json.RawMessage, error) {
	var v any
	switch len(outs) {
	case 0:
		return nil, nil
	case 1:
		v = outs[0].Interface()
	default:
		all := make([]any, len(outs))
		for i, o := range outs {
			all[i] = o.Interface()
		}
		v = all
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return StripAddresses(raw)
}

// Decode reads a result into dsts, one pointer for each value the method
// answers besides an error.
func (c Call) Decode(result json.RawMessage, dsts ...any) error {
	if len(dsts) != len(c.out) {
		return fmt.Errorf("protocol: %s answers %d values, not %d", c.Name, len(c.out), len(dsts))
	}
	switch len(dsts) {
	case 0:
		return nil
	case 1:
		if err := strictUnmarshal(result, dsts[0]); err != nil {
			return fmt.Errorf("%w: %s result: %v", wire.ErrProtocol, c.Name, err)
		}
		return nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(result, &raws); err != nil || len(raws) != len(dsts) {
		return fmt.Errorf("%w: %s answers %d values", wire.ErrProtocol, c.Name, len(dsts))
	}
	for i, raw := range raws {
		if err := strictUnmarshal(raw, dsts[i]); err != nil {
			return fmt.Errorf("%w: %s result %d: %v", wire.ErrProtocol, c.Name, i, err)
		}
	}
	return nil
}

// strictUnmarshal refuses a field the type does not have: it is the other
// side speaking another protocol, not something to skip.
func strictUnmarshal(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
