package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/appkey"
	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/settings"
)

// askedHost answers a window's calls about the host's settings from a
// HostEnd over a file of its own, the way the host across a connection
// would, and keeps what it was asked.
type askedHost struct {
	end  *HostEnd
	sets []wire.SetParams
	lost bool
}

func (h *askedHost) HostSettings(context.Context) (wire.HostSettings, error) {
	if h.lost {
		return wire.HostSettings{}, errors.New("host lost")
	}
	return h.end.Settings(), nil
}

func (h *askedHost) SetHostSetting(_ context.Context, key string, value json.RawMessage) (wire.SetResult, error) {
	if h.lost {
		return wire.SetResult{}, errors.New("host lost")
	}
	h.sets = append(h.sets, wire.SetParams{Key: key, Value: value})
	return h.end.SetSetting(wire.SetParams{Key: key, Value: value}), nil
}

func configFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte("telegram:\n  api_id: 1\n  api_hash: x\n"+body), 0o600))
	return path
}

// windowOn is a window reading its own settings from a file with own, attached
// to a host reading its own from a file with host.
func windowOn(t *testing.T, own, host string) (*WindowSettings, *askedHost, string) {
	t.Helper()
	hostStore, err := config.NewStore(configFile(t, host), t.TempDir())
	require.NoError(t, err)
	asked := &askedHost{end: NewHostEnd(hostStore, testHello)}
	ownPath := configFile(t, own)
	ownStore, err := config.NewStore(ownPath, t.TempDir())
	require.NoError(t, err)
	w := NewWindowSettings(ownStore, asked)
	w.Refresh()
	return w, asked, ownPath
}

// A window shows every setting, in the order they are declared, its own and
// its host's alike (#315).
func TestWindowSettings_ShowsEverySettingInOrder(t *testing.T) {
	w, _, _ := windowOn(t, "", "")

	var got, want []string
	for _, e := range w.Entries() {
		got = append(got, e.Key)
	}
	for _, e := range config.Settings() {
		want = append(want, e.Key)
	}
	assert.Equal(t, want, got)
}

// A host setting is worth what the host holds, whatever the window's own file
// says; a window's setting is worth what its own file says.
func TestWindowSettings_EachSettingIsReadWhereItBelongs(t *testing.T) {
	w, _, _ := windowOn(t,
		"ui:\n  history_limit: 300\n  confirm_quit: false\n",
		"ui:\n  history_limit: 120\n  confirm_quit: true\n")

	limit, status := w.Value("ui.history_limit")
	assert.Equal(t, 120, limit)
	assert.Equal(t, settings.Saved, status)
	quit, _ := w.Value("ui.confirm_quit")
	assert.Equal(t, false, quit)
	assert.False(t, w.IsDefault("ui.history_limit"))
	assert.True(t, w.IsDefault("ui.notifications.desktop"))
}

// A host setting changed from a window goes to the host and is never written
// into the window's file, and the row then shows what the host holds.
func TestWindowSettings_AHostSettingIsChangedByTheHost(t *testing.T) {
	w, asked, ownPath := windowOn(t, "", "")
	before, err := os.ReadFile(ownPath)
	require.NoError(t, err)

	require.NoError(t, w.Set("ui.history_limit", 120))

	require.Len(t, asked.sets, 1)
	assert.JSONEq(t, "120", string(asked.sets[0].Value))
	after, err := os.ReadFile(ownPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	got, _ := w.Value("ui.history_limit")
	assert.Equal(t, 120, got)
}

// What the host refused is refused on the row, in the host's words.
func TestWindowSettings_TheHostsRefusalIsTheRowsRefusal(t *testing.T) {
	w, _, _ := windowOn(t, "", "")

	err := w.Set("ui.history_limit", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 1")
}

// A reset of a host setting crosses as nothing.
func TestWindowSettings_AResetOfAHostSettingCrossesAsNothing(t *testing.T) {
	w, asked, _ := windowOn(t, "", "ui:\n  history_limit: 120\n")

	require.NoError(t, w.Set("ui.history_limit", nil))

	require.Len(t, asked.sets, 1)
	assert.Nil(t, asked.sets[0].Value)
	assert.True(t, w.IsDefault("ui.history_limit"))
}

// A window's own setting is written into its own file, and the host is not
// asked about it.
func TestWindowSettings_AWindowsSettingIsItsOwn(t *testing.T) {
	w, asked, ownPath := windowOn(t, "", "")

	require.NoError(t, w.Set("ui.confirm_quit", false))

	assert.Empty(t, asked.sets)
	raw, err := os.ReadFile(ownPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "confirm_quit: false")
}

// A host that cannot be asked leaves its settings unknown rather than
// pretending to know them, and a change to one is refused.
func TestWindowSettings_AHostThatCannotBeAskedLeavesItsSettingsUnknown(t *testing.T) {
	w, asked, _ := windowOn(t, "", "")
	asked.lost = true
	w.Refresh()

	_, status := w.Value("ui.history_limit")
	assert.Equal(t, settings.Unknown, status)
	assert.Error(t, w.Set("ui.history_limit", 120))
}

// The whole way across: a window of another tele, reading its own settings
// from a file of its own, changes a host setting over the socket, and it is
// the host's owner that runs on it (#315).
func TestWindowSettings_AHostSettingChangedFromAWindowReachesTheHostsOwner(t *testing.T) {
	h := testHost(t)
	own, err := config.NewStore(configFile(t, ""), t.TempDir())
	require.NoError(t, err)
	w := NewWindowSettings(own, h.client)
	w.Refresh()

	require.NoError(t, w.Set("ui.history_limit", 120))

	assert.Eventually(t, func() bool {
		return h.host.current().owner.Config().UI.HistoryLimit == 120
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, 50, own.Current().UI.HistoryLimit, "the window's own file is not where it went")
}

// A window's reload asks its host to read its file again, and hears what the
// host found wrong with its own settings there.
func TestHostReload_HearsWhatTheHostFound(t *testing.T) {
	h := testHost(t)
	path := h.host.cfgStore.Path()
	require.NoError(t, os.WriteFile(path, []byte("ui:\n  history_limit: 0\n"), 0o600))

	got, err := hostReload(h.client)()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "ui.history_limit")
	assert.Equal(t, settings.Host, got[0].Process)
}

func TestHostReload_SaysWhyTheHostDidNotTakeItsFile(t *testing.T) {
	h := testHost(t)
	require.NoError(t, os.WriteFile(h.host.cfgStore.Path(), []byte("ui: [\n"), 0o600))

	_, err := hostReload(h.client)()

	assert.Error(t, err)
}

// A value the host describes rather than shows is shown as described.
func TestWindowSettings_ADescribedValueIsShownAsDescribed(t *testing.T) {
	// The host runs on a built-in key: its file names none.
	hostStore := config.NewStoreOf(&config.Config{}, configFile(t, ""), t.TempDir())
	hostStore.SetKeySource(appkey.SourceInjected)
	asked := &askedHost{end: NewHostEnd(hostStore, testHello)}
	ownStore, err := config.NewStore(configFile(t, ""), t.TempDir())
	require.NoError(t, err)
	w := NewWindowSettings(ownStore, asked)
	w.Refresh()

	got, status := w.Value("telegram.api_id")
	assert.Equal(t, settings.Described("built-in (official build)"), got)
	assert.Equal(t, settings.Saved, status)
}
