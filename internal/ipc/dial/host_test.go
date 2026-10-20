package dial_test

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/serve"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
)

// hostEnd answers the calls about the host with what it was given, and keeps
// what it was asked.
type hostEnd struct {
	hello    wire.Hello
	settings wire.HostSettings
	set      wire.SetResult
	reload   wire.ReloadResult

	mu   sync.Mutex
	sets []wire.SetParams
}

func (h *hostEnd) Hello() wire.Hello           { return h.hello }
func (h *hostEnd) Settings() wire.HostSettings { return h.settings }
func (h *hostEnd) Reload() wire.ReloadResult   { return h.reload }
func (h *hostEnd) SetSetting(p wire.SetParams) wire.SetResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sets = append(h.sets, p)
	return h.set
}

// connectTo is an attachment over a connection to host, with no owner behind
// the attachment: the calls about the host are answered all the same, as on
// the login screen or after a log out.
func connectTo(t *testing.T, host *hostEnd) *dial.Attachment {
	t.Helper()
	client, end := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = serve.Conn(ctx, end, core.NewAttachment(), host)
	}()
	a, err := dial.Dial(ctx, client, hello)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = a.Close()
		cancel()
		<-served
	})
	return a
}

// The host's settings cross as they are: the file it reads them from, and each
// one's value and whether the file names it (#315).
func TestHost_SettingsCross(t *testing.T) {
	host := &hostEnd{hello: hello, settings: wire.HostSettings{
		Config: "/home/a/.config/tele/config.yml",
		Settings: []wire.Setting{
			{Key: "ui.history_limit", Value: json.RawMessage("120")},
			{Key: "ui.notifications.desktop", Value: json.RawMessage("true"), Default: true},
		},
	}}
	a := connectTo(t, host)

	got, err := a.HostSettings(context.Background())

	require.NoError(t, err)
	assert.Equal(t, host.settings, got)
}

// A change to a host setting reaches the host as the key and the value, and
// what the host said of it comes back: warnings, or a refusal, which is an
// answer rather than a failure of the call.
func TestHost_SetCrossesBothWays(t *testing.T) {
	host := &hostEnd{hello: hello, set: wire.SetResult{
		Refused:  "must be at least 1",
		Warnings: []wire.Warning{{Text: "ui.history_limit: repaired", ID: "x"}},
	}}
	a := connectTo(t, host)

	got, err := a.SetHostSetting(context.Background(), "ui.history_limit", json.RawMessage("0"))

	require.NoError(t, err)
	assert.Equal(t, host.set, got)
	require.Len(t, host.sets, 1)
	assert.Equal(t, "ui.history_limit", host.sets[0].Key)
	assert.JSONEq(t, "0", string(host.sets[0].Value))
}

// A reset is a value of nothing, and it crosses as one.
func TestHost_ResetCrossesAsNothing(t *testing.T) {
	host := &hostEnd{hello: hello}
	a := connectTo(t, host)

	_, err := a.SetHostSetting(context.Background(), "ui.history_limit", nil)

	require.NoError(t, err)
	require.Len(t, host.sets, 1)
	assert.Nil(t, host.sets[0].Value)
}

// A reload is answered with what the host found in its file.
func TestHost_ReloadCrosses(t *testing.T) {
	host := &hostEnd{hello: hello, reload: wire.ReloadResult{
		Failed:   "yaml: line 3: mapping values are not allowed",
		Warnings: []wire.Warning{{Text: "proxy.server: ignored"}},
	}}
	a := connectTo(t, host)

	got, err := a.ReloadHost(context.Background())

	require.NoError(t, err)
	assert.Equal(t, host.reload, got)
}

// What was wrong with the host's settings when it started is said in its
// hello, so the first window to attach shows it.
func TestHost_HelloCarriesTheHostsWarnings(t *testing.T) {
	said := hello
	said.Warnings = []wire.Warning{{Text: "telegram.session_file is deprecated"}}
	a := connectTo(t, &hostEnd{hello: said})

	assert.Equal(t, said.Warnings, a.Host().Warnings)
}
