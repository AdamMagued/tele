package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/service"
)

// exe is the name the build named name has on disk here.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// binary writes an executable file at path.
func binary(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	return path
}

// onPath makes dir the whole PATH for the length of the test.
func onPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
}

// A package manager puts a versioned file behind a name on the PATH. The
// service runs the name, so that the next upgrade replaces what it starts
// (#182, ADR 0031).
func TestEntryPoint_IsTheNameOnThePathAsItIs(t *testing.T) {
	root := t.TempDir()
	running := binary(t, filepath.Join(root, "Cellar", "tele", "2.0.0", "bin", exe("tele")))
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	link := filepath.Join(bin, exe("tele"))
	if err := os.Symlink(running, link); err != nil {
		t.Skipf("symlinks need privileges here: %v", err)
	}
	onPath(t, bin)

	got, err := service.EntryPoint("tele", running, "")

	require.NoError(t, err)
	assert.Equal(t, link, got)
}

// A name on the PATH that is another file is not run in this one's place:
// the service would start a tele other than the one installing it.
func TestEntryPoint_AnotherFileOnThePathIsRefused(t *testing.T) {
	root := t.TempDir()
	running := binary(t, filepath.Join(root, "here", exe("tele")))
	other := binary(t, filepath.Join(root, "bin", exe("tele")))
	onPath(t, filepath.Dir(other))

	_, err := service.EntryPoint("tele", running, "")

	var notSame *service.NotOnPathError
	require.True(t, errors.As(err, &notSame), "got %v", err)
	assert.Equal(t, other, notSame.OnPath)
	assert.Equal(t, running, notSame.Running)
	assert.Contains(t, err.Error(), other)
	assert.Contains(t, err.Error(), running)
	assert.Contains(t, err.Error(), "--path")
}

// A tele nowhere on the PATH says so, and how to name it instead.
func TestEntryPoint_NothingOnThePath(t *testing.T) {
	root := t.TempDir()
	running := binary(t, filepath.Join(root, "here", exe("tele")))
	onPath(t, filepath.Join(root, "empty"))

	_, err := service.EntryPoint("tele", running, "")

	var notOnPath *service.NotOnPathError
	require.True(t, errors.As(err, &notOnPath), "got %v", err)
	assert.Empty(t, notOnPath.OnPath)
	assert.Contains(t, err.Error(), "--path")
}

// A path given by the person is theirs to vouch for: it is taken as given,
// as long as it is there.
func TestEntryPoint_APathGivenIsTaken(t *testing.T) {
	root := t.TempDir()
	running := binary(t, filepath.Join(root, "here", exe("tele")))
	given := binary(t, filepath.Join(root, "elsewhere", exe("tele")))

	got, err := service.EntryPoint("tele", running, given)

	require.NoError(t, err)
	assert.Equal(t, given, got)

	_, err = service.EntryPoint("tele", running, filepath.Join(root, "missing", exe("tele")))
	assert.Error(t, err)
}
