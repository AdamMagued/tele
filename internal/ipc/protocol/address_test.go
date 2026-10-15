package protocol_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ipc/protocol"
	"github.com/sorokin-vladimir/tele/internal/ui"
)

// addressFields are the fields an address is kept under, as the protocol
// promises them: written out here rather than taken from the code under test,
// which could drop one and still agree with itself.
var addressFields = []string{"AccessHash", "FileReference"}

// notAddresses are fields that look like an address and are not one. A field
// lands here only after someone decided that, so a new field shaped like an
// address is a question rather than a leak.
var notAddresses = map[string]bool{
	"MediaRef.Waveform": true, // the voice note's amplitudes, drawn by the client
}

// Nothing that can reach a client carries an address out. The test walks every
// type a client can be sent - deltas, windows, events and what each method of
// the client's interface answers with - and holds each to two rules:
//
//   - filled with non-zero values everywhere, it leaves without an address;
//   - a field shaped like an address - an int64 named for a hash, or bytes - is
//     either an address field, which leaves stripped, or named as not one.
//
// The second rule is what catches an address under a name the first would
// not strip.
func TestStripAddresses_NothingSentCarriesAnAddress(t *testing.T) {
	roots := []reflect.Type{reflect.TypeFor[project.Delta]()}
	roots = append(roots, protocol.Windows.Members()...)
	roots = append(roots, protocol.Events.Members()...)
	owner := reflect.TypeFor[ui.Owner]()
	for i := range owner.NumMethod() {
		m := owner.Method(i).Type
		for j := range m.NumOut() {
			if out := m.Out(j); out != reflect.TypeFor[error]() {
				roots = append(roots, out)
			}
		}
	}

	addresses := map[string]bool{}
	for _, f := range addressFields {
		addresses[f] = true
	}
	seen := map[reflect.Type]bool{}
	for _, root := range roots {
		walkFields(root, seen, func(owner reflect.Type, f reflect.StructField) {
			if !looksLikeAddress(f) || addresses[f.Name] {
				return
			}
			name := owner.Name() + "." + f.Name
			assert.True(t, notAddresses[name],
				"%s looks like an address: strip it, or say in notAddresses why it is not one", name)
		})

		v := reflect.New(root).Elem()
		fill(v, 0)
		raw, err := json.Marshal(v.Interface())
		require.NoError(t, err, root)
		stripped, err := protocol.StripAddresses(raw)
		require.NoError(t, err, root)
		for _, f := range addressFields {
			assert.NotContains(t, string(stripped), `"`+f+`"`, "%v leaves with %s", root, f)
		}
	}
}

func looksLikeAddress(f reflect.StructField) bool {
	switch {
	case f.Type.Kind() == reflect.Int64 && strings.Contains(f.Name, "Hash"):
		return true
	case f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Uint8:
		return true
	}
	return false
}

// walkFields calls visit for every exported field of every struct reachable
// from t, once per struct.
func walkFields(t reflect.Type, seen map[reflect.Type]bool, visit func(reflect.Type, reflect.StructField)) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		walkFields(t.Elem(), seen, visit)
	case reflect.Map:
		walkFields(t.Key(), seen, visit)
		walkFields(t.Elem(), seen, visit)
	case reflect.Struct:
		if seen[t] {
			return
		}
		seen[t] = true
		for _, f := range reflect.VisibleFields(t) {
			if !f.IsExported() || f.Anonymous {
				continue
			}
			visit(t, f)
			walkFields(f.Type, seen, visit)
		}
	}
}

// fill sets every settable value under v to something non-zero, so that an
// address field nobody strips shows up in what is encoded. Interfaces stay
// nil: none of them is encoded with an address under it.
func fill(v reflect.Value, depth int) {
	if depth > 8 {
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.String:
		v.SetString("x")
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			fill(v.Index(i), depth+1)
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		e := reflect.New(v.Type().Elem()).Elem()
		fill(k, depth+1)
		fill(e, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Field(i); f.CanSet() {
				fill(f, depth+1)
			}
		}
	}
}

// What Telegram needs to reach a chat or a file stays with the owner: a
// message leaves without its access hashes and file references, and with
// everything else it had.
func TestStripAddresses_AMessageLeavesWithoutItsAddresses(t *testing.T) {
	msg := domain.Message{
		ID:     42,
		ChatID: -1002147483647123,
		// Past 2^53, where a number read as a float loses its last digit.
		SenderID: 9007199254740993,
		Text:     "hi @ann",
		Entities: []domain.MessageEntity{
			{Type: "mention_name", Offset: 3, Length: 4, UserID: 77, AccessHash: 9001},
		},
		Photo: &domain.PhotoRef{ID: 1, AccessHash: 9002, FileReference: []byte{1, 2, 3}, DCID: 2},
		Document: &domain.DocumentRef{
			ID: 2, AccessHash: 9003, FileReference: []byte{4, 5}, DCID: 4, FileName: "a.ogg",
		},
		Media: &domain.MediaRef{Waveform: []byte{7, 8, 9}},
	}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	stripped, err := protocol.StripAddresses(raw)
	require.NoError(t, err)

	assert.NotContains(t, string(stripped), "AccessHash")
	assert.NotContains(t, string(stripped), "FileReference")

	var got domain.Message
	require.NoError(t, json.Unmarshal(stripped, &got))
	want := msg
	want.Entities = []domain.MessageEntity{{Type: "mention_name", Offset: 3, Length: 4, UserID: 77}}
	want.Photo = &domain.PhotoRef{ID: 1, DCID: 2}
	want.Document = &domain.DocumentRef{ID: 2, DCID: 4, FileName: "a.ogg"}
	assert.Equal(t, want, got)
}
