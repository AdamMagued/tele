package protocol_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/ipc/protocol"
)

// Every method of the surface crosses a connection, and nothing else does. The
// calls are written out rather than read off the interface (reading them off
// it doubles the binary, #208), so this is what keeps the two in step: a
// method added to the surface and not to the calls fails here, not on a
// connection.
func TestCalls_AreEveryMethodOfTheSurface(t *testing.T) {
	surface := reflect.TypeFor[core.Surface]()
	var want []string
	for i := range surface.NumMethod() {
		want = append(want, surface.Method(i).Name)
	}
	var got []string
	for name := range protocol.Calls {
		got = append(got, name)
	}
	slices.Sort(want)
	slices.Sort(got)

	assert.Equal(t, want, got)
}
