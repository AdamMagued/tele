package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// addressFields are the fields an address is kept under: what Telegram needs,
// beyond an id, to reach a chat, a person or a file. Only the owner holds
// addresses, so none of them reaches a client (#209).
var addressFields = []string{"AccessHash", "FileReference"}

// StripAddresses is raw with every address field removed, at any depth. A
// client reading it gets the zero value there, which is what it would hold had
// it never been sent one.
//
// It works on the encoded value rather than on the value itself: what the
// owner sends shares memory with what it holds, and the owner's copy keeps its
// addresses. Numbers pass through as written, so a 64-bit id keeps every digit.
func StripAddresses(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("strip addresses: %w", err)
	}
	return json.Marshal(stripAddresses(v))
}

func stripAddresses(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for _, f := range addressFields {
			delete(v, f)
		}
		for k, e := range v {
			v[k] = stripAddresses(e)
		}
	case []any:
		for i, e := range v {
			v[i] = stripAddresses(e)
		}
	}
	return v
}
