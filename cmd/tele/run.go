package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/accountstate"
	"github.com/sorokin-vladimir/tele/internal/app"
	"github.com/sorokin-vladimir/tele/internal/appkey"
	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/ipc"
	"github.com/sorokin-vladimir/tele/internal/ipc/dial"
	"github.com/sorokin-vladimir/tele/internal/ipc/wire"
	"github.com/sorokin-vladimir/tele/internal/statedir"
	"github.com/sorokin-vladimir/tele/internal/version"
)

// helloTimeout bounds how long a client waits for its host to answer hello.
const helloTimeout = 5 * time.Second

// stopGrace is how long a stopping host gives its other clients to hear it.
const stopGrace = 2 * time.Second

// run is what a start has settled by the time it knows whether it hosts.
type run struct {
	cfgStore      *config.Store
	cfgPath       string
	key           appkey.Key
	log           *zap.Logger
	logPath       string
	verbose       bool
	trace         bool
	themeWarnings []string
	reached       hosting.Reached
}

// hello is what this process says of itself on the endpoint, as a client and
// as a host alike.
func (r run) hello() wire.Hello {
	return wire.Hello{Protocol: wire.ProtocolVersion, Binary: version.Version, Config: r.cfgPath}
}

// host runs this process as the host, with its own TUI attached to it through
// the endpoint like any other client (#208). It holds the state directory from
// here to the end. Clients are served before anything else is done, so a tele
// starting beside this one attaches rather than waits; they are seated at the
// first owner when it is open.
func (r run) host() error {
	cfg := r.cfgStore.Current()
	clients := hosting.NewClients()
	hello := r.hello()
	hello.Kept = wire.KeptByWindow
	end := app.NewHostEnd(r.cfgStore, hello)
	srv := hosting.Serve(context.Background(), r.reached.Listener, end, clients)
	// In this order: the endpoint goes, so nobody new attaches and the others
	// hear the host is stopping; then the account stops; the lock goes last.
	var host *app.Host
	defer func() {
		srv.Stop(stopGrace)
		if host != nil {
			host.Stop()
		}
		r.reached.Close()
	}()

	// A pinned session_file is left exactly where the user put it, so nothing
	// is migrated in that case.
	stateMoved := false
	if !cfg.SessionPinned {
		var err error
		stateMoved, err = statedir.Migrate(cfg.StateDir, filepath.Dir(r.cfgPath), r.log)
		if err != nil {
			return fmt.Errorf("state migration: %w", err)
		}
	}
	cleared, reason, err := accountstate.Reconcile(cfg.StateDir, cfg.Telegram.SessionFile)
	if err != nil {
		return fmt.Errorf("account state: %w", err)
	}
	if cleared {
		r.log.Info("cleared local state before startup", zap.String("reason", string(reason)))
	}

	host, err = app.NewHost(r.cfgStore, r.key, r.log, r.trace, clients)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	end.ApplyTo(host.Apply)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	host.Start(ctx)

	att, err := r.attach(r.reached.Endpoint)
	if err != nil {
		return err
	}
	return r.runClient(att, app.ClientConfig{
		// This window is one of the host's clients; the rest close with it.
		OtherWindows: func() int { return max(clients.Count()-1, 0) },
		Notices:      app.StartupNotices(cfg.StateDir, stateMoved, cfg.SessionPinned),
	})
}

// client runs this process as a client of a host another tele keeps, over the
// connection reaching it found. Nothing of the account is opened here: no
// lock, no migration, no owner. The host's settings are its own, from its own
// file, and this client changes them only by asking it (#315).
func (r run) client() error {
	ctx, cancel := context.WithTimeout(context.Background(), helloTimeout)
	defer cancel()
	att, err := dial.Dial(ctx, r.reached.Conn, r.hello())
	if err != nil {
		return errors.New(reachFailure(err, r.cfgStore.Current().StateDir))
	}
	return r.runClient(att, app.ClientConfig{
		Notices: app.StartupNotices(r.cfgStore.Current().StateDir, false, false),
	})
}

// attach reaches the host at ep the way any client does.
func (r run) attach(ep ipc.Endpoint) (*dial.Attachment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helloTimeout)
	defer cancel()
	conn, err := ipc.Dial(ctx, ep)
	if err != nil {
		return nil, fmt.Errorf("reaching this tele's own host: %w", err)
	}
	att, err := dial.Dial(ctx, conn, r.hello())
	if err != nil {
		return nil, fmt.Errorf("attaching to this tele's own host: %w", err)
	}
	return att, nil
}

// runClient draws the account over att until the person quits. role is what
// differs between a client of this process's own host and one of another
// tele's: how it reloads, whose settings it shows, what quitting closes and
// which notices it may show; the rest is filled in here.
func (r run) runClient(att *dial.Attachment, role app.ClientConfig) error {
	role.Store = r.cfgStore
	role.Log = r.log
	role.Attachment = att
	role.Detach = func() { _ = att.Close() }
	role.StateDir = r.cfgStore.Current().StateDir
	role.ThemeWarnings = r.themeWarnings
	role.LogPath = r.logPath
	role.Verbose = r.verbose
	if err := app.NewClient(role).Run(); err != nil {
		return fmt.Errorf("error: %w", err)
	}
	return nil
}
