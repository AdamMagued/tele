package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/accountstate"
	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/notices"
	"github.com/sorokin-vladimir/tele/internal/ui"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// The host's side of an account's life (#297). The host opens an account,
// builds the model that draws it and runs both; when the account ends with a
// log out, the host stops it whole, removes it from disk, opens the next one
// and hands the client that one's model. Nothing of an account lives in the
// host, which is what lets the next one start with nothing of the last.

// current is the account running now and the epoch it has on screen.
func (a *App) current() (*account, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.acct, a.epoch
}

// openNext opens the next account over the state directory and makes it the
// current one, with the next epoch. Its scratch files live in a directory of
// its own, so they go with it rather than pass to whoever logs in next.
func (a *App) openNext() (*account, uint64, error) {
	a.mu.Lock()
	epoch := a.epoch + 1
	a.mu.Unlock()

	cfg := a.cfg()
	tmpDir := ""
	if a.tmpDir != "" {
		tmpDir = filepath.Join(a.tmpDir, "account-"+strconv.FormatUint(epoch, 10))
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
		// The account identity is needed both by the message list (own
		// messages) and by the farewell banner on exit.
		onAuth: func(userID int64, username string) {
			if err := accountstate.Record(a.startup.stateDir, a.startup.sessionFile); err != nil {
				a.log.Error("record account identity", zap.Error(err))
			}
			components.SetSelfIdentity(userID, username)
			a.setSelf(userID, username)
		},
	})
	if err != nil {
		return nil, 0, err
	}
	// The client attaches: the subscriptions and the focus it reports belong
	// to it, everything else is the owner's (#192, #309). It ends with the
	// owner.
	acct.client = acct.owner.Attach()

	a.mu.Lock()
	a.acct, a.epoch = acct, epoch
	a.mu.Unlock()
	return acct, epoch, nil
}

// buildRoot builds the model that draws acct. The person's things - config,
// key map, log path - are the host's and shared; everything the model reaches
// the account through is this account's. The one-time startup notices belong
// to the start of the process, so only the first account's model has them.
func (a *App) buildRoot(acct *account, first bool) ui.RootModel {
	cfg := a.cfg()
	root := ui.NewRootModel(a.verbose)
	root = root.WithContext(acct.ctx).WithImageMode(acct.startup.imageMode).WithConfig(cfg).WithKeyMap(a.keyMap).WithOwner(acct.client).
		WithLogger(a.log).WithConfigReload(a.reloadConfig).WithSettingsStore(a.cfgStore).WithLogPath(a.logPath)
	root.SetLoginModel(screens.NewLoginModel())
	root.SetTmpDir(acct.tmpDir)
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

// send delivers msg to the model of epoch, and to no other: a late message
// from an account that ended is dropped on arrival.
func (a *App) send(epoch uint64, msg tea.Msg) {
	a.deliver(ui.Stamped(epoch, msg))
}

// launch runs acct with the work the host does for it: the bridge to its model,
// and listening for the client to ask for the account to end.
func (a *App) launch(ctx context.Context, acct *account, epoch uint64) {
	acct.run(ctx, a.connectionEnded(ctx, acct, epoch),
		a.deltaBridge(acct, epoch), a.endRequests(ctx, acct, epoch))
}

// endRequests ends acct when its client asks: for an end the connection did not
// bring about itself, such as leaving a banned account (#297).
func (a *App) endRequests(hostCtx context.Context, acct *account, epoch uint64) func(context.Context) {
	return func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case reason := <-acct.owner.EndRequests():
			// On a goroutine of its own: ending the account waits for this
			// work, which is the account's.
			go a.endAccount(hostCtx, acct, epoch, reason)
		}
	}
}

// connectionEnded is told what acct's connection ended with. A log out ends the
// account and starts the next one. Anything else is kept for the exit code;
// the account shows it to its clients while the program still runs, since
// waiting for the exit to report it meant nobody saw it, on a screen nobody
// could quit (#283, #309).
func (a *App) connectionEnded(ctx context.Context, acct *account, epoch uint64) func(error) {
	return func(err error) {
		if ctx.Err() == nil {
			if reason, ok := core.EndReasonOf(err); ok {
				// On a goroutine of its own: ending the account waits for this
				// one, which is still the connection's.
				go a.endAccount(ctx, acct, epoch, reason)
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

// endAccount ends acct for reason and starts the next account in its place:
// it stops acct whole, removes it from disk with its session, opens the next
// one and puts that one's login on screen with the reason it is there (#297,
// ADR 0023 in the notes). If the removal fails, the next account is not
// opened: starting over the last one's files is what this exists to prevent.
func (a *App) endAccount(ctx context.Context, acct *account, epoch uint64, reason core.EndReason) {
	a.switching.Lock()
	defer a.switching.Unlock()
	if ctx.Err() != nil {
		return // the process is ending; its exit stops the account
	}
	discarded := acct.owner.UnsentCount()
	acct.stop()

	if err := accountstate.End(acct.startup.stateDir, acct.startup.sessionFile); err != nil {
		a.log.Error("the account that ended could not be removed", zap.Error(err))
		a.send(epoch, ui.ConnectFailedMsg{Err: fmt.Errorf("the account that ended could not be removed: %w", err)})
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

	next, nextEpoch, err := a.openNext()
	if err != nil {
		a.log.Error("the next account could not be opened", zap.Error(err))
		a.send(epoch, ui.ConnectFailedMsg{Err: err})
		return
	}
	a.deliver(ui.AccountEndedMsg{
		Epoch: nextEpoch,
		Root:  a.buildRoot(next, false),
		Ended: core.AccountEnded{Reason: reason, Discarded: discarded},
	})
	a.launch(ctx, next, nextEpoch)
}

// deltaBridge carries what is addressed to acct's client to its model: the
// client's one stream, deltas and events alike.
func (a *App) deltaBridge(acct *account, epoch uint64) func(context.Context) {
	return func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-acct.client.Stream():
				a.send(epoch, msg)
			}
		}
	}
}
