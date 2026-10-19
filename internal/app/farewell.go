package app

import (
	"fmt"
	"strings"

	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/version"
)

// farewellBanner is what tele leaves in the scrollback after the TUI closes:
// the splash logo, the build it ran, the account it ran as, and where this
// run's log went. Facts that are unknown (no login happened, no log path was
// set) are dropped rather than printed empty.
//
// It is deliberately uncolored: the alternate screen is gone by the time this
// prints, so the terminal's own background is back and unknown to us.
func farewellBanner(ver, account, logPath string) string {
	var b strings.Builder
	b.WriteString("\n")
	for _, line := range components.LogoLines() {
		b.WriteString(strings.TrimRight(line, " "))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	id := versionLabel(ver)
	if account != "" {
		id += " · " + account
	}
	fmt.Fprintf(&b, "  %s\n", id)
	if logPath != "" {
		fmt.Fprintf(&b, "  log  %s\n", logPath)
	}
	b.WriteString("\n")
	return b.String()
}

// versionLabel formats the build version like the status bar does: local builds
// keep their "dev" marker, releases get a "v" prefix.
func versionLabel(v string) string {
	if v == "" || v == "dev" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// accountLabel names the account the session ran as. Telegram accounts need not
// have a username, so the numeric id is the fallback.
func accountLabel(userID int64, username string) string {
	switch {
	case username != "":
		return "@" + username
	case userID != 0:
		return fmt.Sprintf("id %d", userID)
	}
	return ""
}

// shortenHome replaces a leading home directory with "~", but only on a path
// boundary so /home/user2 is not mangled into ~2.
func shortenHome(path, home string) string {
	if home == "" || path == home {
		return path
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

// farewell renders the banner for this run, naming the account as the client
// last heard it.
func (c *Client) farewell(home string) string {
	c.heardMu.Lock()
	user := c.user
	c.heardMu.Unlock()
	return farewellBanner(version.Version, accountLabel(user.ID, user.Username), shortenHome(c.c.LogPath, home))
}
