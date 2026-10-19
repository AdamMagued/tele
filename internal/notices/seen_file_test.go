package notices_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/notices"
)

// A notice is about the machine rather than the account, so what was seen is
// kept apart from the account's database, which goes with every log out (#297).
func TestFileSeen_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.json")

	seen, existed := notices.NewFileSeen(path)
	assert.False(t, existed)
	assert.False(t, seen.IsSeen("some-notice"))
	seen.MarkSeen("some-notice")
	assert.True(t, seen.IsSeen("some-notice"))

	reopened, existed := notices.NewFileSeen(path)
	assert.True(t, existed)
	assert.True(t, reopened.IsSeen("some-notice"))
	assert.False(t, reopened.IsSeen("other-notice"))
}

// A file that cannot be read is a notice shown once more, never a start that
// fails over it.
func TestFileSeen_AnUnreadableFileSeesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))

	seen, _ := notices.NewFileSeen(path)

	assert.False(t, seen.IsSeen("some-notice"))
	seen.MarkSeen("some-notice")
	reopened, _ := notices.NewFileSeen(path)
	assert.True(t, reopened.IsSeen("some-notice"), "marking rewrites the file whole")
}

// Two windows on one host are two processes over one file. Each one marking a
// notice seen keeps what the other marked: a dismissal in one window must not
// bring a notice back in the next start (#208).
func TestFileSeen_TwoWindowsKeepWhatTheOtherMarked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.json")
	first, _ := notices.NewFileSeen(path)
	second, _ := notices.NewFileSeen(path)

	first.MarkSeen("a")
	second.MarkSeen("b")

	reopened, _ := notices.NewFileSeen(path)
	assert.True(t, reopened.IsSeen("a"), "the first window's mark was kept")
	assert.True(t, reopened.IsSeen("b"))
}

// What an earlier build recorded in the account's database is carried over
// once, so an upgrade does not show every notice again.
func TestCarryOver_CopiesWhatWasSeenBefore(t *testing.T) {
	old := notices.NewMemorySeen()
	old.MarkSeen("a")
	seen, _ := notices.NewFileSeen(filepath.Join(t.TempDir(), "notices.json"))

	notices.CarryOver(seen, old, []string{"a", "b"})

	assert.True(t, seen.IsSeen("a"))
	assert.False(t, seen.IsSeen("b"))
}
