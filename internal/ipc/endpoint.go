// Package ipc is how a client reaches a host: a unix domain socket, on every
// platform tele runs on, Windows included (#205). It moves bytes and nothing
// else; what the bytes say is package wire's, and who may serve the endpoint is
// the hosting code's, which holds the state directory lock while it decides.
package ipc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Endpoint is where a host serves its clients: one per state directory, so
// two accounts in two state directories never answer each other's clients.
type Endpoint struct {
	// Path is the socket file.
	Path string
	// own is the directory made for the socket alone, which Listen creates
	// private to the user; empty when the socket lives in the state directory,
	// which exists and is private already.
	own string
}

// EndpointFor is the endpoint of the host over stateDir, for the build named
// app ("tele", "tele-beta"). On Linux it lives in the runtime directory, an
// in-memory one emptied at logout, so stale sockets do not pile up; elsewhere,
// or without one, in the state directory. A path too long for the platform's
// sockets falls back to the temporary directory, under the same hash.
func EndpointFor(app, stateDir string) (Endpoint, error) {
	return endpointFor(app, stateDir, runtime.GOOS, os.Getenv("XDG_RUNTIME_DIR"), os.TempDir())
}

func endpointFor(app, stateDir, goos, runtimeDir, tmpDir string) (Endpoint, error) {
	resolved, err := resolve(stateDir)
	if err != nil {
		return Endpoint{}, fmt.Errorf("the state directory for the socket: %w", err)
	}
	hash := stateHash(resolved)

	ep := Endpoint{Path: filepath.Join(resolved, "owner.sock")}
	if goos == "linux" && runtimeDir != "" {
		own := filepath.Join(runtimeDir, app)
		ep = Endpoint{Path: filepath.Join(own, hash+".sock"), own: own}
	}
	if len(ep.Path) <= maxPath(goos) {
		return ep, nil
	}
	own := filepath.Join(tmpDir, app+"-"+hash)
	fallback := Endpoint{Path: filepath.Join(own, "owner.sock"), own: own}
	if len(fallback.Path) > maxPath(goos) {
		return Endpoint{}, fmt.Errorf("no socket path short enough for %s: %s is %d bytes, %s is %d, and sockets here take at most %d",
			resolved, ep.Path, len(ep.Path), fallback.Path, len(fallback.Path), maxPath(goos))
	}
	return fallback, nil
}

// resolve is dir as an absolute path with its symlinks followed, so the same
// directory reached two ways is one endpoint.
func resolve(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// stateHash names a state directory in twelve hex digits.
func stateHash(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(sum[:6])
}

// maxPath is the longest socket path the platform takes: the size of sun_path
// less its terminating zero.
func maxPath(goos string) int {
	switch goos {
	case "linux", "windows":
		return 107
	default:
		return 103
	}
}
