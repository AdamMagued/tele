package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

// Frame is one thing off an attachment's stream as it crosses, less its
// addresses: a delta addressed to its subscription, or an event under its
// name. Anything else on a stream is a bug.
func Frame(v any) (wire.Frame, error) {
	switch v := v.(type) {
	case project.Delta:
		raw, err := encodeStripped(v)
		if err != nil {
			return wire.Frame{}, err
		}
		return wire.Delta(uint64(v.Sub), raw)
	case core.Event:
		name, raw, err := Events.Encode(v)
		if err != nil {
			return wire.Frame{}, err
		}
		if raw, err = StripAddresses(raw); err != nil {
			return wire.Frame{}, err
		}
		return wire.Event(name, raw)
	}
	return wire.Frame{}, fmt.Errorf("protocol: %T is neither a delta nor an event", v)
}

// Unframe is what a delta or an event frame carried, as the attachment's
// stream held it on the other side.
func Unframe(f wire.Frame) (any, error) {
	if f.Event != "" {
		return Events.Decode(f.Event, f.Params)
	}
	var d project.Delta
	if err := strictUnmarshal(f.Delta, &d); err != nil {
		return nil, fmt.Errorf("%w: delta: %v", wire.ErrProtocol, err)
	}
	// The frame says whose it is; the payload's own copy of that is not
	// trusted over it.
	d.Sub = project.SubID(f.Sub)
	return d, nil
}

func encodeStripped(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return StripAddresses(raw)
}
