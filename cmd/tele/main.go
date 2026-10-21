package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sorokin-vladimir/tele/internal/appkey"
	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
	"github.com/sorokin-vladimir/tele/internal/version"
)

// Injected at build time via -ldflags. Fall back to config file values if zero.
var (
	buildAPIID   = "0"
	buildAPIHash = ""
	appName      = "tele" // injected via -ldflags for the beta channel
)

// options are the flags every command shares.
type options struct {
	cfgPath string
	verbose bool
	trace   bool
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newRootCmd is tele: the window with no command, and the commands beside it.
func newRootCmd() *cobra.Command {
	var o options
	var themeCheck, themeDump optionalArg
	var printVersion bool
	root := &cobra.Command{
		Use:           appName,
		Short:         "A terminal Telegram client",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			if printVersion {
				fmt.Println(version.Version)
				return nil
			}
			return runWindow(o, themeCheck, themeDump)
		},
	}
	// The command set is not settled yet; nothing is to be completed against
	// it before it is.
	root.CompletionOptions.DisableDefaultCmd = true

	pf := root.PersistentFlags()
	pf.StringVar(&o.cfgPath, "config", defaultConfigPath(appName), "path to config file")
	pf.BoolVarP(&o.verbose, "debug", "e", false, "debug logging")
	pf.BoolVar(&o.trace, "trace", false, "log sensitive metadata (peer IDs, message lengths) — never use in shared environments")

	f := root.Flags()
	// A flag of its own rather than cobra's: cobra's prints more than the
	// version, and a version template of one's own is executed by
	// text/template, which keeps every method of every type the binary links
	// (#208).
	f.BoolVar(&printVersion, "version", false, "print version and exit")
	f.Var(&themeCheck, "theme-check", "print which theme each slot resolved to and where its tokens came from, then exit; give a theme name to inspect one")
	f.Lookup("theme-check").NoOptDefVal = bareArg
	f.Var(&themeDump, "theme-dump", "print a slot's theme (dark or light) as a complete theme file, then exit; give a theme name to dump one")
	f.Lookup("theme-dump").NoOptDefVal = bareArg

	root.AddCommand(newDaemonCmd(&o))
	return root
}

// setup is what every command that runs tele settles first: the config file,
// the store over it, and where the log lives.
type setup struct {
	cfgPath string
	store   *config.Store
	// logDir is the platform state home, where the log goes whichever state
	// directory the account was pinned to.
	logDir string
}

// newSetup reads the config file at cfgPath, writing a default one first when
// there is none.
func newSetup(cfgPath string) (setup, error) {
	cfgPath = config.ExpandTilde(cfgPath)
	if err := ensureConfig(cfgPath); err != nil {
		return setup{}, fmt.Errorf("config: %w", err)
	}
	logDir, err := stateDirPath(appName)
	if err != nil {
		return setup{}, fmt.Errorf("state dir: %w", err)
	}
	// The store rather than a bare config: it holds the file's path, which is
	// what a reload needs and what the settings overlay writes to.
	store, err := config.NewStore(cfgPath, logDir)
	if err != nil {
		return setup{}, fmt.Errorf("config: %w", err)
	}
	return setup{cfgPath: cfgPath, store: store, logDir: logDir}, nil
}

// appKey settles the app key once, beside the config rather than in it: the
// config is what the file says, and a reload would lose a key written into it
// (#239).
func (s setup) appKey() (appkey.Key, error) {
	cfg := s.store.Current()
	injectedID, _ := strconv.Atoi(buildAPIID)
	key, source, err := appkey.Resolve(
		appkey.Key{ID: cfg.Telegram.APIID, Hash: cfg.Telegram.APIHash},
		appkey.Key{ID: injectedID, Hash: buildAPIHash},
	)
	switch {
	case errors.Is(err, appkey.ErrHalfKey):
		return appkey.Key{}, fmt.Errorf("config: set both telegram.api_id and telegram.api_hash in %s, or neither", s.cfgPath)
	case err != nil:
		return appkey.Key{}, fmt.Errorf("config: set telegram.api_id and telegram.api_hash in %s\nGet credentials at https://my.telegram.org", s.cfgPath)
	}
	s.store.SetKeySource(source)
	return key, nil
}

// makeLogDir makes the directory the log goes in. It is per-machine
// diagnostic output, not account data.
func (s setup) makeLogDir() error {
	if err := os.MkdirAll(s.logDir, 0700); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	return nil
}

const defaultConfigHead = `telegram:
  api_id: 0
  api_hash: ""

ui:
  history_limit: 50
  confirm_quit: false  # ask before q, ctrl+c or ctrl+q closes tele
  # Themes follow the terminal background: a dark one and a light one, each
  # named here. Leave this out for the built-in tele-dark and tele-light. See
  # docs/themes.md.
  # theme:
  #   dark: my-dark
  #   light: my-light

photos:
  eager_full_quality: true  # download full resolution in background on chat open

# Keybindings — every action with its current default keys (see docs/keybindings.md).
# Uncomment a line and change its key(s) to override that action in that context.
# One key replaces the defaults; a chord is space-separated tokens ("g g" = g then g).
`

// defaultConfig is the head plus the generated, fully-commented keybindings
// reference, so the written config stays in sync with the actual defaults.
func defaultConfig() string {
	return defaultConfigHead + keys.DefaultKeybindingsYAML()
}

// defaultConfigPath returns the default config file location for the given app
// name, e.g. ~/.config/tele/config.yml (or ~/.config/tele-beta/config.yml for
// the beta channel). The tilde is expanded later by expandTilde.
func defaultConfigPath(app string) string {
	return filepath.Join("~", ".config", app, "config.yml")
}

// stateDirPath returns $XDG_STATE_HOME/<app>, falling back to
// ~/.local/state/<app>. It does not create the directory: the state directory
// is created by reaching its host, and the log directory by its own caller.
func stateDirPath(app string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, app), nil
}

// ensureConfig creates a default config file if it does not exist.
func ensureConfig(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfig()), 0600)
}
