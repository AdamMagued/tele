package main

import (
	"fmt"
	"strings"

	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/ui/theme"
)

// optionalArg is a flag that may be given bare (--theme-dump) or with a value
// (--theme-dump=light). A bare occurrence arrives at Set as bareArg, the
// flag's NoOptDefVal, which is how "given, with no value" is spelled here.
type optionalArg struct {
	set   bool
	value string
}

// bareArg is what a bare optionalArg is set to.
const bareArg = "true"

func (o *optionalArg) String() string { return o.value }
func (o *optionalArg) Type() string   { return "theme" }

func (o *optionalArg) Set(s string) error {
	o.set = true
	if s != bareArg {
		o.value = strings.TrimSpace(s)
	}
	return nil
}

// themeReport answers the two questions a theme file raises that the screen
// cannot: which theme am I actually running, and which of these colors did I
// set rather than inherit.
func themeReport(themesDir string, themes theme.Loaded, arg string, warnings []config.Warning) string {
	var b strings.Builder
	fmt.Fprintf(&b, "themes directory: %s\n\n", themesDir)

	switch strings.ToLower(arg) {
	case "":
		b.WriteString(theme.Report("dark", themes.Dark))
		b.WriteString("\n")
		b.WriteString(theme.Report("light", themes.Light))
		if themes.Dark.Theme.Name == themes.Light.Theme.Name {
			fmt.Fprintf(&b, "\nBoth slots hold %s, so the terminal's background makes no difference.\n",
				themes.Dark.Theme.Name)
		}
	case "dark":
		b.WriteString(theme.Report("dark", themes.Dark))
	case "light":
		b.WriteString(theme.Report("light", themes.Light))
	default:
		res, _ := resolveNamed(themesDir, arg)
		b.WriteString(theme.Report("named", res))
	}

	if len(warnings) > 0 {
		b.WriteString("\nwarnings:\n")
		for _, w := range warnings {
			fmt.Fprintf(&b, "  %s\n", w.Text)
		}
	}
	return b.String()
}

// themeDumpText writes a theme out as a complete file, ready to be redirected
// into the themes directory and edited. Only the file goes to stdout; anything
// that went wrong is the caller's to report, so the output stays loadable.
func themeDumpText(themesDir string, themes theme.Loaded, arg string) (string, error) {
	switch strings.ToLower(arg) {
	case "", "dark":
		return theme.Dump(themes.Dark), nil
	case "light":
		return theme.Dump(themes.Light), nil
	default:
		res, ok := resolveNamed(themesDir, arg)
		if !ok {
			return "", fmt.Errorf("no theme named %q in %s", arg, themesDir)
		}
		return theme.Dump(res), nil
	}
}

// resolveNamed resolves a theme by name for the diagnostics, which are not tied
// to a slot. A theme that does not name a base of its own is resolved against
// tele-dark; the report prints the chain, so which root was used is visible.
func resolveNamed(themesDir, name string) (theme.Resolved, bool) {
	loaded := theme.LoadSlots(themesDir, name, "")
	return loaded.Dark, strings.EqualFold(loaded.Dark.Theme.Name, name)
}
