package wire_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

type shape interface{ isShape() }

type circle struct{ R int }
type square struct{ Side int }
type triangle struct{}

func (circle) isShape()   {}
func (square) isShape()   {}
func (triangle) isShape() {}

func shapes() *wire.Kinds[shape] {
	k := wire.NewKinds[shape]()
	wire.Register[circle](k, "circle")
	wire.Register[square](k, "square")
	return k
}

// A member crosses under its name and comes back as what it was.
func TestKinds_AMemberRoundTrips(t *testing.T) {
	k := shapes()

	name, raw, err := k.Encode(square{Side: 3})
	require.NoError(t, err)
	assert.Equal(t, "square", name)

	got, err := k.Decode(name, raw)
	require.NoError(t, err)
	assert.Equal(t, square{Side: 3}, got)
}

// A name the family does not have is the other side speaking another
// protocol, not something to skip.
func TestKinds_AnUnknownNameIsAProtocolError(t *testing.T) {
	_, err := shapes().Decode("triangle", []byte(`{}`))

	assert.ErrorIs(t, err, wire.ErrProtocol)
}

func TestKinds_AnUnknownFieldIsAProtocolError(t *testing.T) {
	_, err := shapes().Decode("circle", []byte(`{"R":1,"Colour":"red"}`))

	assert.ErrorIs(t, err, wire.ErrProtocol)
}

func TestKinds_AValueWithNoNameIsRefused(t *testing.T) {
	k := wire.NewKinds[shape]()
	wire.Register[circle](k, "circle")

	_, _, err := k.Encode(square{})

	assert.Error(t, err)
}

// Registering is done once, when the protocol is put together: a clash is a
// bug.
func TestKinds_AClashPanics(t *testing.T) {
	k := shapes()
	assert.Panics(t, func() { wire.Register[circle](k, "round") }, "a second name")
	assert.Panics(t, func() { wire.Register[triangle](k, "circle") }, "a taken name")
	assert.Panics(t, func() { wire.Register[int](wire.NewKinds[shape](), "int") }, "not a member")
}
