// Package ipctest holds what the connection's tests share.
package ipctest

import (
	"reflect"
	"strconv"
)

// Fill sets every settable value under v to something non-zero, so that a
// field that is lost on the way, or an address nobody strips, shows up in what
// arrives. Each value gets a number of its own, so two values swapped on the
// way show up too. Interfaces stay nil: none of them is filled the same way
// twice.
func Fill(v reflect.Value) {
	var f Filler
	f.Fill(v)
}

// Filler fills several values with numbers none of them shares, such as the
// arguments of one call.
type Filler struct{ n int }

// Fill is Fill, numbering on from the values f filled before.
func (f *Filler) Fill(v reflect.Value) { fill(v, 0, &f.n) }

// Filled is a T with every field set.
func Filled[T any]() T {
	var v T
	Fill(reflect.ValueOf(&v).Elem())
	return v
}

func fill(v reflect.Value, depth int, n *int) {
	if depth > 8 {
		return
	}
	*n++
	// Small enough for every integer kind, and never zero.
	num := *n%100 + 1
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(num))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(num))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(num))
	case reflect.String:
		v.SetString("x" + strconv.Itoa(num))
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), depth+1, n)
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), depth+1, n)
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			fill(v.Index(i), depth+1, n)
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		e := reflect.New(v.Type().Elem()).Elem()
		fill(k, depth+1, n)
		fill(e, depth+1, n)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Field(i); f.CanSet() {
				fill(f, depth+1, n)
			}
		}
	}
}
