package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gotd/td/telegram/updates"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/appkey"
	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/notices"
	"github.com/sorokin-vladimir/tele/internal/proxy"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

// Host runs owners, one account after another, for as long as the process
// lives, and seats every client it has at each owner in turn (#297, #208). It
// draws nothing: the clients do, each in its own process or beside the host in
// this one.
type Host struct {
	// cfgStore is the config file and the config the host is running on. The
	// host asks it rather than holding a Config of its own, so that a reload
	// reaches every account through one place.
	cfgStore *config.Store
	log      *zap.Logger
	// startup is what the process settled when it started, handed to every
	// account it opens in place of what the file says by then (#239).
	startup startup

	// Nothing of an account lives in the host (#297). acct is the one running
	// now, changed under mu when an account ends and the next one starts;
	// opened counts the accounts opened, for naming their scratch directories.
	mu     sync.Mutex
	acct   *account
	opened uint64
	// clients is every attachment the host has, seated at each account's owner
	// in turn (#309, #208).
	clients *hosting.Clients
	// switching serialises the end of one account and the start of the next
	// with the stop of whichever is running when the host stops.
	switching sync.Mutex

	// What the host needs to open each account: how to connect to Telegram and
	// where desktop notifications go.
	connect  func(internaltg.Endpoint, updates.StateStorage) core.Connection
	notifier core.Notifier

	// tmpDir holds the host's scratch files: the media cache when the user
	// asked for no persistent one, and each account's own directory inside it,
	// removed when the account ends. Removed when the host stops.
	tmpDir string
}

// NewHost builds the host over the config the process started with and the
// app key it resolved, and opens the first account, seating clients at its
// owner. The key is handed in rather than read from the config: it is settled
// from the file and from how the binary was built, and the file alone does not
// know the second (#239).
func NewHost(cfgStore *config.Store, key appkey.Key, log *zap.Logger, trace bool, clients *hosting.Clients) (*Host, error) {
	start, err := newStartup(cfgStore.Current(), key)
	if err != nil {
		return nil, err
	}
	if err := checkRoute(start.route, cfgStore.Path(), log); err != nil {
		return nil, err
	}

	// The media cache may live in the temp directory, and the owner needs the
	// cache before it starts.
	tmpDir, err := os.MkdirTemp("", "tele-*")
	if err != nil {
		log.Warn("failed to create temp dir for media", zap.Error(err))
		tmpDir = ""
	}
	removeLegacyMediaCache(log)

	h := &Host{
		cfgStore: cfgStore,
		log:      log,
		startup:  start,
		clients:  clients,
		connect: func(endpoint internaltg.Endpoint, stateStorage updates.StateStorage) core.Connection {
			return internaltg.NewGotdClient(log, endpoint, stateStorage, trace)
		},
		notifier: newNotifier(log),
		tmpDir:   tmpDir,
	}
	acct, err := h.openNext()
	if err != nil {
		return nil, err
	}
	h.carryOverNotices(acct)
	return h, nil
}

// Start runs the current account, and ends it when a client asks.
func (h *Host) Start(ctx context.Context) {
	go h.listen(ctx)
	h.launch(ctx, h.current())
}

// Stop stops whichever account is running, and not while another is taking its
// place, then removes the host's scratch files.
func (h *Host) Stop() {
	h.switching.Lock()
	h.current().stop()
	h.switching.Unlock()
	if h.tmpDir != "" {
		_ = os.RemoveAll(h.tmpDir)
	}
}

// Apply hands cfg, which the host has just read, to the owner running now. An
// owner opened later starts from the store as it is then.
func (h *Host) Apply(cfg *config.Config) {
	h.current().owner.SetConfig(cfg)
}

// cfg is the config the host is running on. Asked for rather than held, so
// that a reload is seen by whoever asks next.
func (h *Host) cfg() *config.Config { return h.cfgStore.Current() }

// carryOverNotices brings the machine's record of the notices already shown
// out of acct's database, where an earlier build kept it, into the file every
// client reads, the first time the file is made. The database is the host's,
// so this is the host's to do, once, before any client reads the file.
func (h *Host) carryOverNotices(acct *account) {
	seen, existed := notices.NewFileSeen(filepath.Join(h.startup.stateDir, NoticesFile))
	if existed {
		return
	}
	notices.CarryOver(seen, notices.NewSQLiteSeen(acct.store.DB()), noticeIDs())
}

// checkRoute settles how this process will reach Telegram, before anything is
// drawn. Both things it does belong before the interface: the route is named in
// the log, and a declared proxy is dialled once so a server nobody is listening
// on is a message on the terminal rather than an endless reconnect.
func checkRoute(route proxy.Route, path string, log *zap.Logger) error {
	// One line, at Info so it shows without -e. It names the address, because a
	// wrong port has to be visible in a log somebody pastes into an issue, and
	// never the secret or the password.
	log.Info("telegram route", zap.String("proxy", route.Describe()))

	if err := proxy.Probe(context.Background(), route); err != nil {
		return fmt.Errorf("%w (set proxy.type: direct in %s to connect without a proxy)", err, path)
	}
	return nil
}
