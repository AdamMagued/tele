package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/accountstate"
	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/notices"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	"github.com/sorokin-vladimir/tele/internal/ui"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// The host's side of an account's life (#297). The host opens an account and
// runs it, and seats the client's attachment at its owner; when the account
// ends with a log out, the host tells the client, stops the account whole,
// removes it from disk, opens the next one and seats the client there. Nothing
// of an account lives in the host, which is what lets the next one start with
// nothing of the last. The client outlives the accounts as the host does, and
// draws each one itself (#309).

// newClients is the host's clients with the TUI's own attachment among them.
func newClients() (*hosting.Clients, *core.Attachment) {
	clients := hosting.NewClients()
	client := core.NewAttachment()
	clients.Add(client)
	return clients, client
}

// current is the account running now.
func (a *App) current() *account {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.acct
}

// openNext opens the next account over the state directory, makes it the
// current one and seats the client at its owner. Its scratch files live in a
// directory of its own, so they go with it rather than pass to whoever logs in
// next.
func (a *App) openNext() (*account, error) {
	a.mu.Lock()
	a.opened++
	n := a.opened
	a.mu.Unlock()

	cfg := a.cfg()
	tmpDir := ""
	if a.tmpDir != "" {
		tmpDir = filepath.Join(a.tmpDir, "account-"+strconv.FormatUint(n, 10))
		if err := os.MkdirAll(tmpDir, 0o700); err != nil {
			a.log.Warn("no scratch directory for the account", zap.Error(err))
			tmpDir = ""
		}
	}
	acct, err := openAccount(accountDeps{
		cfg:      cfg,
		startup:  a.startup,
		log:      a.log,
		tmpDir:   tmpDir,
		notifier: a.notifier,
		connect:  a.connect,
		// The session identity is recorded once the login succeeds. The
		// logged-in user reaches the model through the account projection;
		// the farewell banner still reads it here.
		onAuth: func(userID int64, username string) {
			if err := accountstate.Record(a.startup.stateDir, a.startup.sessionFile); err != nil {
				a.log.Error("record account identity", zap.Error(err))
			}
			a.setSelf(userID, username)
		},
	})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.acct = acct
	a.mu.Unlock()
	a.clients.Join(acct.owner)
	return acct, nil
}

// buildRoot builds a model for the client to draw whichever account it is
// attached to. The person's things - config, key map, log path - are shared;
// what the model saves to show the person goes in a scratch directory of its
// own, which the next model's replaces. The one-time startup notices belong to
// the start of the process, so only the first model has them.
func (a *App) buildRoot(ctx context.Context, first bool) ui.RootModel {
	cfg := a.cfg()
	root := ui.NewRootModel(a.verbose)
	root = root.WithContext(ctx).WithImageMode(a.startup.imageMode).WithConfig(cfg).WithKeyMap(a.keyMap).WithOwner(a.client).
		WithLogger(a.log).WithConfigReload(a.reloadConfig).WithSettingsStore(a.cfgStore).WithLogPath(a.logPath)
	root.SetLoginModel(screens.NewLoginModel())
	root.SetTmpDir(a.nextScreenDir())
	if first {
		// Seen-state is written on dismissal, so quitting before the countdown
		// ends shows the notice again next time (#197).
		root = root.WithNotices(notices.Pending(a.pendingNotices(), a.noticeSeen), a.noticeSeen)
		// The themes loaded at startup may have been reloaded since, and a
		// reload says what is wrong with them itself.
		root = root.WithThemeWarnings(a.themeWarnings)
	}
	return root
}

// nextScreenDir makes the scratch directory of the next model and removes the
// last one's: documents opened and photos saved for an account that ended are
// that account's, and must not outlive it on disk.
func (a *App) nextScreenDir() string {
	if a.tmpDir == "" {
		return ""
	}
	if a.screenDir != "" {
		_ = os.RemoveAll(a.screenDir)
	}
	a.screens++
	dir := filepath.Join(a.tmpDir, "screen-"+strconv.FormatUint(a.screens, 10))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		a.log.Warn("no scratch directory for the screen", zap.Error(err))
		dir = ""
	}
	a.screenDir = dir
	return dir
}

// openNoticeSeen opens the machine's record of the notices already shown. An
// earlier build kept it in the account's database, which a log out removes;
// what that database says is carried over once, the first time the file is
// made, so an upgrade shows nothing twice.
func (a *App) openNoticeSeen(acct *account) notices.Seen {
	seen, existed := notices.NewFileSeen(filepath.Join(a.startup.stateDir, "notices.json"))
	if !existed {
		var ids []string
		for _, n := range a.pendingNotices() {
			ids = append(ids, n.ID)
		}
		notices.CarryOver(seen, notices.NewSQLiteSeen(acct.store.DB()), ids)
	}
	return seen
}

// bridge carries the client's stream to its model for the life of the
// process: deltas and events from whichever owner it is seated at, and what
// the host tells it. The stream is one and ordered, so whatever arrives after
// the news that an account ended belongs to the next one.
func (a *App) bridge(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-a.client.Stream():
			a.prog.Send(msg)
		}
	}
}

// listen ends the current account when the client asks: for an end the
// connection did not bring about itself, such as leaving a banned account
// (#297).
func (a *App) listen(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-a.clients.EndRequests():
			go a.endAccount(ctx, a.current(), reason)
		}
	}
}

// launch runs acct, listening for what its connection ends with.
func (a *App) launch(ctx context.Context, acct *account) {
	acct.run(ctx, a.connectionEnded(ctx, acct))
}

// connectionEnded is told what acct's connection ended with. A log out ends the
// account and starts the next one. Anything else is kept for the exit code;
// the account shows it to its clients while the program still runs, since
// waiting for the exit to report it meant nobody saw it, on a screen nobody
// could quit (#283, #309).
func (a *App) connectionEnded(ctx context.Context, acct *account) func(error) {
	return func(err error) {
		if ctx.Err() == nil {
			if reason, ok := core.EndReasonOf(err); ok {
				// On a goroutine of its own: ending the account waits for this
				// one, which is still the connection's.
				go a.endAccount(ctx, acct, reason)
				return
			}
		}
		a.mu.Lock()
		a.exitErr = err
		a.mu.Unlock()
	}
}

func (a *App) lastExitErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitErr
}

// endAccount ends acct for reason and starts the next account in its place.
// The client hears it first, before anything is closed: its calls in flight
// are answered as ended rather than failing on whatever the stop closes under
// them. Then the account is stopped whole, removed from disk with its session,
// and the next one is opened and the client seated there (#297, #309). If the
// removal fails, the next account is not opened: starting over the last one's
// files is what this exists to prevent.
//
// Only the account running now can end: a second request for one that has
// already ended - the connection and the client both asking - would otherwise
// remove the next account's files.
func (a *App) endAccount(ctx context.Context, acct *account, reason core.EndReason) {
	a.switching.Lock()
	defer a.switching.Unlock()
	if ctx.Err() != nil {
		return // the process is ending; its exit stops the account
	}
	if a.current() != acct {
		return
	}
	discarded := acct.owner.UnsentCount()
	a.clients.Leave(core.AccountEnded{Reason: reason, Discarded: discarded})
	acct.stop()

	if err := accountstate.End(acct.startup.stateDir, acct.startup.sessionFile); err != nil {
		a.log.Error("the account that ended could not be removed", zap.Error(err))
		a.clients.Tell(core.HostFailure{Err: telerr.From(fmt.Errorf("the account that ended could not be removed: %w", err))})
		return
	}
	if acct.tmpDir != "" {
		_ = os.RemoveAll(acct.tmpDir)
	}
	a.log.Info("account ended", zap.String("reason", string(reason)), zap.Int("discarded", discarded))
	// What the ended account's connection ended with is not the process's
	// to exit on.
	a.mu.Lock()
	a.exitErr = nil
	a.mu.Unlock()

	next, err := a.openNext()
	if err != nil {
		a.log.Error("the next account could not be opened", zap.Error(err))
		a.clients.Tell(core.HostFailure{Err: telerr.From(err)})
		return
	}
	a.launch(ctx, next)
}
