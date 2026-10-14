package wire_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

func hello(binary string, protocol int) wire.Hello {
	return wire.Hello{Protocol: protocol, Binary: binary}
}

func service(binary string, protocol int) wire.Hello {
	h := hello(binary, protocol)
	h.Service = true
	return h
}

// The four cases a version handshake has to cover, and the ones around them.
func TestDecide(t *testing.T) {
	for name, c := range map[string]struct {
		client, host wire.Hello
		want         wire.Verdict
	}{
		"same binary": {hello("2.0.0", 1), hello("2.0.0", 1), wire.Proceed},

		// A service follows its newer side.
		"newer client, service":         {hello("2.1.0", 1), service("2.0.0", 1), wire.RestartHost},
		"newer client, service, beta":   {hello("2.0.0", 1), service("2.0.0-beta.3", 1), wire.RestartHost},
		"newer client, service, broken": {hello("2.1.0", 2), service("2.0.0", 1), wire.RestartHost},
		// An old client left open across an upgrade must not take it back.
		"older client, service": {hello("2.0.0", 1), service("2.1.0", 1), wire.RestartClient},

		// Someone's session is never restarted from outside.
		"newer CLI, terminal host, same protocol":       {hello("2.1.0", 1), hello("2.0.0", 1), wire.Proceed},
		"newer CLI, terminal host, another protocol":    {hello("2.1.0", 2), hello("2.0.0", 1), wire.Refuse},
		"older client, terminal host, another protocol": {hello("2.0.0", 1), hello("2.1.0", 2), wire.Refuse},

		// A local build cannot be ordered, so only the protocol decides.
		"local build, service":                   {hello("dev", 1), service("2.0.0", 1), wire.Proceed},
		"local build, service, another protocol": {hello("dev", 2), service("2.0.0", 1), wire.Refuse},
	} {
		assert.Equal(t, c.want, wire.Decide(c.client, c.host), name)
	}
}
