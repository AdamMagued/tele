package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// NotOnPathError is a running tele the service cannot be registered by: the
// name on the PATH is another file, or there is none. Registering the running
// file instead would tie the service to one version, and the next upgrade
// would leave it behind (ADR 0031).
type NotOnPathError struct {
	// OnPath is the file the name finds on the PATH, empty when it finds none.
	OnPath  string
	Running string
}

func (e *NotOnPathError) Error() string {
	if e.OnPath == "" {
		return fmt.Sprintf("tele is not on the PATH, so the service would run %s, which an upgrade leaves behind.\nName the command to run with --path.", e.Running)
	}
	return fmt.Sprintf("tele on the PATH is %s, and this is %s: the service would run the other one.\nName the command to run with --path.", e.OnPath, e.Running)
}

// EntryPoint is the command the service of the build named name runs: the
// name as the PATH finds it, kept as it is, symlink and all, so that an
// upgrade replaces what the service starts (#182). It must be the running
// binary, which is what installs the service; given names a command the
// person vouches for instead, taken as it is.
func EntryPoint(name, running, given string) (string, error) {
	if given != "" {
		abs, err := filepath.Abs(given)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("--path: %w", err)
		}
		return abs, nil
	}
	found, err := exec.LookPath(name)
	if errors.Is(err, exec.ErrNotFound) {
		return "", &NotOnPathError{Running: running}
	}
	if err != nil {
		return "", err
	}
	found, err = filepath.Abs(found)
	if err != nil {
		return "", err
	}
	if !sameFile(found, running) {
		return "", &NotOnPathError{OnPath: found, Running: running}
	}
	return found, nil
}

// sameFile reports whether a and b, symlinks followed, are one file.
func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}
