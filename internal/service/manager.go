package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Manager registers units with the operating system's service manager.
type Manager interface {
	// Install writes u and starts the service. A unit already installed under
	// its name is replaced, and the service started anew on it.
	Install(u Unit) error
	// Uninstall stops the service named name and removes its unit. It reports
	// false, and does nothing, when none is installed.
	Uninstall(name string) (bool, error)
}

// Runner runs a command of the service manager's. Its error carries what the
// command said.
type Runner func(name string, args ...string) error

// Run runs a command of the service manager's for real.
func Run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if said := strings.TrimSpace(string(out)); said != "" {
			return fmt.Errorf("%s: %s", name, said)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// ErrUnsupported is a system tele has no service manager for. A host is run
// there by hand, with `tele daemon` (#182).
var ErrUnsupported = errors.New("tele cannot be installed as a service on this system")

// ForThisSystem is the service manager of the system tele runs on, for the
// person whose home is home.
func ForThisSystem(home string) (Manager, error) {
	switch runtime.GOOS {
	case "darwin":
		return Launchd{Home: home, UID: os.Getuid(), Run: Run}, nil
	case "linux":
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		return Systemd{ConfigHome: config, Run: Run}, nil
	case "windows":
		return TaskScheduler{TempDir: os.TempDir(), Run: Run}, nil
	default:
		return nil, ErrUnsupported
	}
}

// Launchd installs a unit as a LaunchAgent: the person's own, run in their
// session while they are logged in.
type Launchd struct {
	Home string
	UID  int
	Run  Runner
}

func (m Launchd) plist(name string) string {
	return filepath.Join(m.Home, "Library", "LaunchAgents", Label(Unit{Name: name})+".plist")
}

func (m Launchd) domain() string { return "gui/" + strconv.Itoa(m.UID) }

func (m Launchd) Install(u Unit) error {
	path := m.plist(u.Name)
	if _, err := os.Stat(path); err == nil {
		// Unloaded first, or launchd keeps running what it loaded before. One
		// that was not loaded has nothing to unload.
		_ = m.Run("launchctl", "bootout", m.domain()+"/"+Label(u))
	}
	if u.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(u.LogPath), 0o700); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, Plist(u), 0o644); err != nil {
		return err
	}
	return m.Run("launchctl", "bootstrap", m.domain(), path)
}

func (m Launchd) Uninstall(name string) (bool, error) {
	path := m.plist(name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	// An agent that is not loaded has nothing to stop; its file still goes.
	_ = m.Run("launchctl", "bootout", m.domain()+"/"+Label(Unit{Name: name}))
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

// Systemd installs a unit as a systemd user unit, enabled for the person's
// login.
type Systemd struct {
	ConfigHome string
	Run        Runner
}

func (m Systemd) unit(name string) string {
	return filepath.Join(m.ConfigHome, "systemd", "user", name+".service")
}

func (m Systemd) Install(u Unit) error {
	path := m.unit(u.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, SystemdUnit(u), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", u.Name + ".service"},
		// Started if it was not running, started anew on the new unit if it
		// was.
		{"--user", "restart", u.Name + ".service"},
	} {
		if err := m.Run("systemctl", args...); err != nil {
			return err
		}
	}
	return nil
}

func (m Systemd) Uninstall(name string) (bool, error) {
	path := m.unit(name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := m.Run("systemctl", "--user", "disable", "--now", name+".service"); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, m.Run("systemctl", "--user", "daemon-reload")
}

// TaskScheduler installs a unit as a Task Scheduler task started at logon: a
// Windows service would run where no notification can be shown (ADR 0030).
type TaskScheduler struct {
	// TempDir is where the task's XML is written for schtasks to read.
	TempDir string
	Run     Runner
}

func (m TaskScheduler) Install(u Unit) error {
	f, err := os.CreateTemp(m.TempDir, "tele-task-*.xml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(TaskXML(u)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Ended first, so the task starts on the command it is given now. One not
	// running has nothing to end.
	_ = m.Run("schtasks", "/End", "/TN", u.Name)
	if err := m.Run("schtasks", "/Create", "/TN", u.Name, "/XML", f.Name(), "/F"); err != nil {
		return err
	}
	return m.Run("schtasks", "/Run", "/TN", u.Name)
}

func (m TaskScheduler) Uninstall(name string) (bool, error) {
	if err := m.Run("schtasks", "/Query", "/TN", name); err != nil {
		return false, nil
	}
	_ = m.Run("schtasks", "/End", "/TN", name)
	if err := m.Run("schtasks", "/Delete", "/TN", name, "/F"); err != nil {
		return false, err
	}
	return true, nil
}
