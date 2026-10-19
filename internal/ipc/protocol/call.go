package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/core/project"
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

	// fn is the method as a function of the surface it is called on.
	fn  reflect.Value
	in  []reflect.Type
	out []reflect.Type
}

// Calls is every method of the surface, by name.
var Calls = calls()

// MethodSubscribe crosses in a shape of its own: the client chooses the
// subscription's id, so the id goes with the window, and nothing comes back -
// the subscription's first delta is its answer. The host takes it as
// SubscribeAs.
const MethodSubscribe = "Subscribe"

func calls() map[string]Call {
	ctxType := reflect.TypeFor[context.Context]()
	errType := reflect.TypeFor[error]()
	all := make(map[string]Call, len(surface))
	for name, method := range surface {
		fn := reflect.ValueOf(method)
		t := fn.Type()
		c := Call{Name: name, fn: fn}
		// The first argument is the surface the method is called on.
		for j := 1; j < t.NumIn(); j++ {
			p := t.In(j)
			if j == 1 && p == ctxType {
				c.Context = true
				continue
			}
			mustCross(name, p)
			c.in = append(c.in, p)
		}
		for j := range t.NumOut() {
			o := t.Out(j)
			c.Answers = true
			if j == t.NumOut()-1 && o == errType {
				continue
			}
			mustCross(name, o)
			c.out = append(c.out, o)
		}
		if name == MethodSubscribe {
			c.in = append([]reflect.Type{reflect.TypeFor[project.SubID]()}, c.in...)
			c.out, c.Answers = nil, false
		}
		all[name] = c
	}
	return all
}

// mustCross refuses a type no connection can carry: a channel, a function, or
// an interface with no family to name its members. The surface is put
// together once, so one that has such a method is a bug, and panics.
func mustCross(method string, t reflect.Type) {
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		panic(fmt.Sprintf("protocol: %s takes or answers %v, which cannot cross a connection", method, t))
	case reflect.Interface:
		if _, ok := families[t]; !ok {
			panic(fmt.Sprintf("protocol: %s takes or answers %v, which has no family to cross in", method, t))
		}
	}
}

// family is a registry of names seen through the interface it names: what a
// call's argument of that interface type crosses with.
type family interface {
	encode(v any) (string, json.RawMessage, error)
	decode(name string, raw json.RawMessage) (any, error)
}

type kindsOf[T any] struct{ k *wire.Kinds[T] }

func (f kindsOf[T]) encode(v any) (string, json.RawMessage, error) {
	t, ok := v.(T)
	if !ok {
		return "", nil, fmt.Errorf("protocol: %T is not a %v", v, reflect.TypeFor[T]())
	}
	return f.k.Encode(t)
}

func (f kindsOf[T]) decode(name string, raw json.RawMessage) (any, error) {
	return f.k.Decode(name, raw)
}

// families are the interfaces a call may take, each with its registry.
var families = map[reflect.Type]family{
	reflect.TypeFor[project.Window](): kindsOf[project.Window]{Windows},
}

// named is a member of a family as it crosses: its name, and itself.
type named struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

// Params encodes args, the arguments a call is made with, without its context.
func (c Call) Params(args ...any) (json.RawMessage, error) {
	if len(args) != len(c.in) {
		return nil, fmt.Errorf("protocol: %s takes %d arguments, not %d", c.Name, len(c.in), len(args))
	}
	out := make([]any, len(args))
	for i, arg := range args {
		out[i] = arg
		f, ok := families[c.in[i]]
		if !ok {
			continue
		}
		name, raw, err := f.encode(arg)
		if err != nil {
			return nil, err
		}
		out[i] = named{Kind: name, Value: raw}
	}
	return json.Marshal(out)
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
		if f, ok := families[t]; ok {
			var n named
			if err := strictUnmarshal(raws[i], &n); err != nil {
				return nil, fmt.Errorf("%w: %s argument %d: %v", wire.ErrProtocol, c.Name, i, err)
			}
			v, err := f.decode(n.Kind, n.Value)
			if err != nil {
				return nil, err
			}
			p := reflect.New(t).Elem()
			p.Set(reflect.ValueOf(v))
			args[i] = p
			continue
		}
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
	// The surface goes first, seen as the interface rather than as whatever
	// type is behind it.
	in := []reflect.Value{reflect.ValueOf(&s).Elem()}
	if c.Context {
		in = append(in, reflect.ValueOf(ctx))
	}
	outs := c.fn.Call(append(in, args...))
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
	return encodeStripped(v)
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
