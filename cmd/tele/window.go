package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/hosting"
	"github.com/sorokin-vladimir/tele/internal/ui/theme"
)

// runWindow is tele with no command: a window on the account, hosting it when
// nobody else does.
func runWindow(o options, themeCheck, themeDump optionalArg) error {
	s, err := newSetup(o.cfgPath)
	if err != nil {
		return err
	}
	cfg := s.store.Current()

	// Themes are resolved here, before the TUI exists, so a bad theme file is
	// reported like a config warning rather than something the interface has to
	// cope with. The TUI is handed the result and never sees the config.
	themesDir := cfg.ThemesDir
	themes := theme.LoadSlots(themesDir, cfg.UI.ThemeSlots.Dark, cfg.UI.ThemeSlots.Light)
	theme.SetSlots(themes.Slots())
	// What is printed and logged at startup: the config's warnings and the
	// themes'. Gathered here rather than written into the config, which is what
	// the file says and is replaced by a reload (#239).
	warnings := slices.Clone(cfg.Warnings)
	for _, w := range themes.Warnings {
		warnings = append(warnings, config.Warning{Text: "theme: " + w})
	}

	if themeCheck.set {
		fmt.Print(themeReport(themesDir, themes, themeCheck.value, warnings))
		return nil
	}
	if themeDump.set {
		out, err := themeDumpText(themesDir, themes, themeDump.value)
		if err != nil {
			return fmt.Errorf("theme: %w", err)
		}
		fmt.Print(out)
		return nil
	}

	key, err := s.appKey()
	if err != nil {
		return err
	}
	if err := s.makeLogDir(); err != nil {
		return err
	}
	// Every start begins by reaching the host of the state directory: a tele
	// already holding it is attached to, and with nobody there this one
	// becomes the host, and then reaches itself the same way (#208).
	reached, err := hosting.Reach(context.Background(), appName, cfg.StateDir)
	if err != nil {
		return errors.New(reachFailure(err, cfg.StateDir))
	}
	isHost := reached.Listener != nil
	log, logPath, closeLog := openLog(s.logDir, isHost, o.verbose)
	defer func() {
		_ = log.Sync()
		_ = closeLog()
	}()

	// The log keeps every warning on every run, including the ones the TUI shows
	// only once: suppressing a toast must not lose the record.
	for _, w := range warnings {
		log.Warn("config: " + w.Text)
		fmt.Fprintf(os.Stderr, "config: %s\n", w.Text)
	}

	r := run{
		cfgStore:      s.store,
		cfgPath:       s.cfgPath,
		key:           key,
		log:           log,
		logPath:       logPath,
		verbose:       o.verbose,
		trace:         o.trace,
		themeWarnings: themes.Warnings,
		reached:       reached,
	}
	if isHost {
		return r.host()
	}
	return r.client()
}
