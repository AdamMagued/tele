package wire_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// mustFrame takes a frame a constructor built from a value that always
// encodes.
func mustFrame(f wire.Frame, err error) wire.Frame {
	if err != nil {
		panic(err)
	}
	return f
}

// Every shape crosses and comes back as itself, under the keys a person reads.
func TestFrame_EveryShapeRoundTrips(t *testing.T) {
	frames := map[wire.Shape]wire.Frame{
		wire.ShapeRequest: mustFrame(wire.Request(7, "Send", map[string]string{"ref": "c8f1"})),
		wire.ShapeReply:   mustFrame(wire.Reply(7, nil)),
		wire.ShapeError:   wire.Fail(7, &telerr.Error{Kind: telerr.RateLimited, RetryAfter: 32 * time.Second}),
		wire.ShapeDelta:   mustFrame(wire.Delta(3, map[string]int{"n": 1})),
		wire.ShapeEvent:   mustFrame(wire.Event("notification", map[string]string{"title": "Ada"})),
		wire.ShapeCancel:  wire.Cancel(7),
	}
	for shape, f := range frames {
		var buf bytes.Buffer
		require.NoError(t, wire.NewEncoder(&buf).Encode(f), "shape %d", shape)

		got, err := wire.NewDecoder(&buf).Decode()
		require.NoError(t, err, "shape %d", shape)
		gotShape, err := got.Shape()
		require.NoError(t, err)
		assert.Equal(t, shape, gotShape)
		assert.Equal(t, f, got)
	}
}

func TestFrame_KeysAreWholeWords(t *testing.T) {
	var buf bytes.Buffer
	f := mustFrame(wire.Request(7, "Send", map[string]string{"ref": "c8f1"}))
	require.NoError(t, wire.NewEncoder(&buf).Encode(f))

	assert.Equal(t, `{"id":7,"method":"Send","params":{"ref":"c8f1"}}`+"\n", buf.String())
}

// A call that answers with nothing still answers: its reply is not mistaken
// for a line with no shape.
func TestFrame_AnEmptyReplyIsAReply(t *testing.T) {
	f := mustFrame(wire.Reply(9, nil))
	shape, err := f.Shape()
	require.NoError(t, err)
	assert.Equal(t, wire.ShapeReply, shape)
	assert.JSONEq(t, `null`, string(f.Result))
}

// An error crosses as the domain error it is, and one that was not becomes the
// Internal error saying what it said: nothing reaches a client as a bare
// string.
func TestFrame_AnErrorCrossesAsADomainError(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, wire.NewEncoder(&buf).Encode(wire.Fail(4, errors.New("boom"))))

	got, err := wire.NewDecoder(&buf).Decode()
	require.NoError(t, err)
	assert.Equal(t, telerr.Internal, got.Error.Kind)
	assert.Equal(t, "boom", got.Error.Detail)
}

func TestFrame_ACancelNamesWhatItCallsOff(t *testing.T) {
	id, err := wire.Cancel(7).CancelledID()
	require.NoError(t, err)
	assert.Equal(t, uint64(7), id)
}

// Lines that are no frame end the connection: once one is misread, nothing
// after it can be trusted to be in order.
func TestDecoder_ALineThatIsNoFrameIsAProtocolError(t *testing.T) {
	for name, line := range map[string]string{
		"not json":              `{"id":`,
		"no shape":              `{}`,
		"null":                  `null`,
		"unknown key":           `{"id":1,"m":"Send"}`,
		"request without id":    `{"method":"Send"}`,
		"reply with params":     `{"id":1,"result":1,"params":{}}`,
		"delta without sub":     `{"delta":{}}`,
		"event with an id":      `{"id":1,"event":"typing"}`,
		"cancel with no target": `{"method":"$cancel","params":{}}`,
		"cancel with an id":     `{"id":3,"method":"$cancel","params":{"id":7}}`,
		"two values":            `{"id":1,"result":1} {"id":2,"result":2}`,
	} {
		_, err := wire.NewDecoder(strings.NewReader(line + "\n")).Decode()
		assert.ErrorIs(t, err, wire.ErrProtocol, name)
	}
}

func TestDecoder_ALineTooLongIsRefusedUnread(t *testing.T) {
	long := `{"id":1,"result":"` + strings.Repeat("a", wire.MaxLine) + `"}` + "\n"

	_, err := wire.NewDecoder(strings.NewReader(long)).Decode()

	assert.ErrorIs(t, err, wire.ErrProtocol)
}

// A connection that ends between frames ends cleanly; one that ends inside a
// line cut a frame in half.
func TestDecoder_TheEndOfTheConnection(t *testing.T) {
	_, err := wire.NewDecoder(strings.NewReader("")).Decode()
	assert.Equal(t, io.EOF, err)

	_, err = wire.NewDecoder(strings.NewReader(`{"id":1,"result":1}`)).Decode()
	assert.ErrorIs(t, err, wire.ErrProtocol)
}

// The encoder will not send what the other side would refuse.
func TestEncoder_RefusesAFrameWithNoShape(t *testing.T) {
	var buf bytes.Buffer

	err := wire.NewEncoder(&buf).Encode(wire.Frame{ID: 1})

	assert.ErrorIs(t, err, wire.ErrProtocol)
	assert.Zero(t, buf.Len())
}

// Writers on several goroutines never interleave inside a line, and every
// frame arrives whole.
func TestEncoder_FramesFromSeveralWritersArriveWhole(t *testing.T) {
	var buf bytes.Buffer
	enc := wire.NewEncoder(&buf)
	var wg sync.WaitGroup
	const writers, each = 8, 200
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				f, _ := wire.Delta(uint64(w+1), map[string]string{"pad": strings.Repeat("x", 300)})
				assert.NoError(t, enc.Encode(f))
			}
		}()
	}
	wg.Wait()

	dec := wire.NewDecoder(&buf)
	n := 0
	for {
		f, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(f.Delta, &payload))
		n++
	}
	assert.Equal(t, writers*each, n)
}

// One writer's frames arrive in the order it wrote them: deltas are stated
// against the one before.
func TestEncoder_KeepsOneWritersOrder(t *testing.T) {
	var buf bytes.Buffer
	enc := wire.NewEncoder(&buf)
	for i := 1; i <= 50; i++ {
		require.NoError(t, enc.Encode(mustFrame(wire.Delta(1, i))))
	}

	dec := wire.NewDecoder(&buf)
	for i := 1; i <= 50; i++ {
		f, err := dec.Decode()
		require.NoError(t, err)
		var got int
		require.NoError(t, json.Unmarshal(f.Delta, &got))
		assert.Equal(t, i, got)
	}
}
