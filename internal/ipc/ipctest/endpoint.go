package ipctest

import (
	"os"
	"path/filepath"
	"testing"
)

// ForgetEndpoint removes, when the test ends, what the endpoint at socket
// left outside its state directory stateDir. That is the socket file, and the
// directory made for it when the directory belongs to this state directory
// alone. The directory named after the build app in the runtime directory is
// every state directory's at once: test binaries of other packages, run beside
// this one, have their live sockets in it, so it is never removed.
func ForgetEndpoint(t *testing.T, app, stateDir, socket string) {
	t.Helper()
	dir := filepath.Dir(socket)
	if dir == stateDir {
		return
	}
	t.Cleanup(func() {
		if filepath.Base(dir) == app {
			_ = os.Remove(socket)
			return
		}
		_ = os.RemoveAll(dir)
	})
}
