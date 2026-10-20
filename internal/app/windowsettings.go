package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/settings"
)

// HostCalls are the calls a window makes about its host's settings.
type HostCalls interface {
	HostSettings(ctx context.Context) (wire.HostSettings, error)
	SetHostSetting(ctx context.Context, key string, value json.RawMessage) (wire.SetResult, error)
}

// hostCallTimeout bounds a call about the host's settings. The host is on this
// machine and answers by reading or writing one file; one that takes longer is
// not answering, and the overlay must not hang on it.
const hostCallTimeout = 2 * time.Second

// WindowSettings is every setting as a window sees it: its own, read from and
// written to its own config file, and its host's, as the host holds them and
// changed only by asking the host (#315). The host's are asked for when the
// overlay opens and again after each change made here, so they are as current
// as a modal needs and no more.
type WindowSettings struct {
	own  *config.Store
	host HostCalls

	mu sync.Mutex
	// held is what the host last answered, by key; nil when it could not be
	// asked.
	held     map[string]wire.Setting
	hostFile string
}

var (
	_ settings.Store      = (*WindowSettings)(nil)
	_ settings.Defaulting = (*WindowSettings)(nil)
)

// NewWindowSettings is the settings of a window reading its own from own,
// attached to a host it asks about the host's through host.
func NewWindowSettings(own *config.Store, host HostCalls) *WindowSettings {
	return &WindowSettings{own: own, host: host}
}

// Refresh asks the host for its settings again. A host that cannot be asked
// leaves them unknown.
func (w *WindowSettings) Refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), hostCallTimeout)
	defer cancel()
	answer, err := w.host.HostSettings(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.held, w.hostFile = nil, ""
		return
	}
	w.held = make(map[string]wire.Setting, len(answer.Settings))
	for _, s := range answer.Settings {
		w.held[s.Key] = s
	}
	w.hostFile = answer.Config
}

// HostFile is the config file the host reads its settings from, or empty when
// it could not be asked.
func (w *WindowSettings) HostFile() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hostFile
}

// Entries is every setting, in the order they are declared.
func (w *WindowSettings) Entries() []settings.Entry { return w.own.Entries() }

// Origin is the window's own file.
func (w *WindowSettings) Origin() string { return w.own.Origin() }

// Value answers a host setting from what the host holds and a window's from
// its own file.
func (w *WindowSettings) Value(key string) (any, settings.Status) {
	if !isHosts(key) {
		return w.own.Value(key)
	}
	w.mu.Lock()
	s, ok := w.held[key]
	w.mu.Unlock()
	if !ok {
		return nil, settings.Unknown
	}
	if s.Described != "" {
		return settings.Described(s.Described), settings.Saved
	}
	v, err := config.DecodeValue(key, s.Value)
	if err != nil {
		return nil, settings.Unknown
	}
	return v, settings.Saved
}

// IsDefault reports that the file the setting is kept in does not name it.
func (w *WindowSettings) IsDefault(key string) bool {
	if !isHosts(key) {
		return w.own.IsDefault(key)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.held[key].Default
}

// Set changes a window's setting in its own file, and asks the host to change
// one of the host's. What the host refused is refused here, in its words.
func (w *WindowSettings) Set(key string, v any) error {
	if !isHosts(key) {
		return w.own.Set(key, v)
	}
	var raw json.RawMessage
	if v != nil {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostCallTimeout)
	defer cancel()
	answer, err := w.host.SetHostSetting(ctx, key, raw)
	if err != nil {
		return errors.New("the tele hosting this account did not answer")
	}
	if answer.Refused != "" {
		return errors.New(answer.Refused)
	}
	w.Refresh()
	return nil
}

// isHosts reports that key is a setting of the host.
func isHosts(key string) bool {
	e, ok := config.Setting(key)
	return ok && e.Process == settings.Host
}
