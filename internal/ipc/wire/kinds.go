package wire

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// Kinds names the members of one family of values that share a place on the
// wire - the windows a client subscribes to, the events it is told - so that
// each crosses under a name and comes back as what it was. A family's names
// are all its own: a name the family does not have is a protocol error, not a
// value to skip.
type Kinds[T any] struct {
	byName map[string]reflect.Type
	byType map[reflect.Type]string
}

func NewKinds[T any]() *Kinds[T] {
	return &Kinds[T]{byName: map[string]reflect.Type{}, byType: map[reflect.Type]string{}}
}

// Register adds V to the family under name. A family is an interface, and V a
// type that implements it. Registering is done once, when the protocol is put
// together, so a clash is a bug and panics.
func Register[V any, T any](k *Kinds[T], name string) {
	t := reflect.TypeFor[V]()
	family := reflect.TypeFor[T]()
	if family.Kind() != reflect.Interface || !t.Implements(family) {
		panic(fmt.Sprintf("wire: %v is not a member of the family %v", t, family))
	}
	if _, taken := k.byName[name]; taken {
		panic(fmt.Sprintf("wire: name %q is taken", name))
	}
	if _, named := k.byType[t]; named {
		panic(fmt.Sprintf("wire: %v already has a name", t))
	}
	k.byName[name] = t
	k.byType[t] = name
}

// Encode gives v's name and v as JSON.
func (k *Kinds[T]) Encode(v T) (string, json.RawMessage, error) {
	t := reflect.TypeOf(v)
	name, ok := k.byType[t]
	if !ok {
		return "", nil, fmt.Errorf("wire: %v has no name in this family", t)
	}
	raw, err := json.Marshal(v)
	return name, raw, err
}

// Decode turns raw back into the member named name.
func (k *Kinds[T]) Decode(name string, raw json.RawMessage) (T, error) {
	var zero T
	t, ok := k.byName[name]
	if !ok {
		return zero, protocolError("no %q in this family", name)
	}
	p := reflect.New(t)
	if len(raw) > 0 {
		if err := strictUnmarshal(raw, p.Interface()); err != nil {
			return zero, protocolError("%q: %v", name, err)
		}
	}
	v, ok := p.Elem().Interface().(T)
	if !ok {
		return zero, protocolError("%q is not a member of this family", name)
	}
	return v, nil
}

// Members lists the types the family has names for, in no particular order:
// what a test holds against what the code declares.
func (k *Kinds[T]) Members() []reflect.Type {
	return slices.Collect(maps.Keys(k.byType))
}
