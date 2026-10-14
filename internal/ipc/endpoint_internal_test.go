// Where the socket goes is worked out the same way on every platform, with the
// platform passed in; these tests spell paths the unix way. On Windows the
// real endpoint is exercised by the socket tests instead.

//go:build !windows

package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortDir is a state directory with a short path, so a test is about where
// the socket goes rather than about how long the test's own path is.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "s")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

// On Linux the socket lives in the runtime directory, which is emptied at
// logout, under a name that is the state directory's.
func TestEndpoint_OnLinuxInTheRuntimeDirectory(t *testing.T) {
	state := shortDir(t)

	ep, err := endpointFor("tele", state, "linux", "/run/user/1000", "/tmp")

	require.NoError(t, err)
	assert.Equal(t, "/run/user/1000/tele/"+stateHash(state)+".sock", ep.Path)
	assert.Equal(t, "/run/user/1000/tele", ep.own)
}

func TestEndpoint_InTheStateDirectoryOtherwise(t *testing.T) {
	state := shortDir(t)
	for _, c := range []struct{ goos, runtimeDir string }{
		{"linux", ""}, {"darwin", ""}, {"freebsd", ""}, {"windows", ""}, {"darwin", "/run/user/1000"},
	} {
		ep, err := endpointFor("tele", state, c.goos, c.runtimeDir, "/tmp")

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(state, "owner.sock"), ep.Path, c.goos)
		assert.Empty(t, ep.own, "the state directory is already private")
	}
}

// Two state directories are two accounts, and never one endpoint, wherever the
// socket lives.
func TestEndpoint_TwoStateDirectoriesNeverShareOne(t *testing.T) {
	a, b := shortDir(t), shortDir(t)

	epA, _ := endpointFor("tele", a, "linux", "/run/user/1000", "/tmp")
	epB, _ := endpointFor("tele", b, "linux", "/run/user/1000", "/tmp")

	assert.NotEqual(t, epA.Path, epB.Path)
}

// The same directory reached through a symlink is the same account.
func TestEndpoint_ASymlinkIsTheDirectoryItPointsTo(t *testing.T) {
	state := shortDir(t)
	link := filepath.Join(shortDir(t), "link")
	require.NoError(t, os.Symlink(state, link))

	viaLink, err := endpointFor("tele", link, "linux", "/run/user/1000", "/tmp")
	require.NoError(t, err)
	direct, err := endpointFor("tele", state, "linux", "/run/user/1000", "/tmp")
	require.NoError(t, err)

	assert.Equal(t, direct.Path, viaLink.Path)
}

// A state directory too deep for a socket path does not cost the person tele:
// the socket goes to the temporary directory, still under the state
// directory's name.
func TestEndpoint_APathTooLongFallsBackToTheTemporaryDirectory(t *testing.T) {
	deep := shortDir(t)
	for len(deep) < 110 {
		deep = filepath.Join(deep, "deep-directory")
	}
	require.NoError(t, os.MkdirAll(deep, 0o700))

	ep, err := endpointFor("tele", deep, "darwin", "", "/tmp")

	require.NoError(t, err)
	assert.Equal(t, "/tmp/tele-"+stateHash(deep)+"/owner.sock", ep.Path)
	assert.Equal(t, "/tmp/tele-"+stateHash(deep), ep.own)
}

// With no short path to be had, the error says what was tried and why not.
func TestEndpoint_NoShortPathSaysWhy(t *testing.T) {
	deep := shortDir(t)
	for len(deep) < 110 {
		deep = filepath.Join(deep, "deep-directory")
	}
	require.NoError(t, os.MkdirAll(deep, 0o700))

	_, err := endpointFor("tele", deep, "darwin", "", "/"+strings.Repeat("t", 100))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most 103")
}

func TestEndpoint_AStateDirectoryThatIsNotThere(t *testing.T) {
	_, err := endpointFor("tele", filepath.Join(shortDir(t), "missing"), "darwin", "", "/tmp")

	assert.Error(t, err)
}
