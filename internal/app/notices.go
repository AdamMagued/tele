package app

import (
	"time"

	"github.com/sorokin-vladimir/tele/internal/notices"
)

// NoticesFile is the machine's record of the one-time notices already shown,
// in the state directory. A notice is about the machine rather than the
// account, so it outlives every log out (#297).
const NoticesFile = "notices.json"

// noticeDelay is how long a notice waits before it shows.
const noticeDelay = 7 * time.Second

// StartupNotices lists the one-time messages this build can show at start.
// Conditional ones are left out when they do not apply, so a person only ever
// sees what happened on their machine. stateMoved and sessionPinned are the
// host's to know: a client that only attaches to another tele's host passes
// false for both, since the window of the tele that moved the state said so.
func StartupNotices(stateDir string, stateMoved, sessionPinned bool) []notices.Notice {
	out := []notices.Notice{
		{
			ID:    "second-window-v2.0",
			Title: "A second tele joins the first",
			Delay: noticeDelay,
			Body: "A second tele on the same account now opens alongside the first and " +
				"shares its connection. It closes when the first one quits.",
		},
	}
	if stateMoved {
		out = append(out, notices.Notice{
			ID:    "state-dir-moved-v1.10",
			Title: "Your data moved",
			Delay: noticeDelay,
			Body: "The session and local database now live in " + stateDir +
				", instead of next to the config file. They were moved for you and " +
				"nothing was lost: you are still logged in. The old location is now empty " +
				"and can be ignored.",
		})
	}
	if sessionPinned {
		out = append(out, notices.Notice{
			ID:    "session-file-deprecated-v1.10",
			Title: "session_file is going away",
			Delay: noticeDelay,
			Body: "Your config sets telegram.session_file, so your session was left exactly " +
				"where it is. That setting is deprecated and will be removed in the next " +
				"release. Replace it with state_dir pointing at the directory that should " +
				"hold the session and the database.",
		})
	}
	return out
}

// noticeIDs is every notice an earlier build may have recorded as seen in an
// account's database, for carrying over into the file once.
func noticeIDs() []string {
	ids := []string{"single-instance-v1.10"}
	for _, n := range StartupNotices("", true, true) {
		ids = append(ids, n.ID)
	}
	return ids
}
