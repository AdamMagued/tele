package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/service"
)

// commands is a service manager's command line as a test plays it: every
// command it was given, and what each one answers. A command whose line
// starts with a key of fail fails.
type commands struct {
	ran  []string
	fail map[string]error
	// read keeps, for a command naming a file, what the file said then.
	read map[string]string
	// file is the argument after which a command names a file to read.
	file string
}

func (c *commands) run(name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	c.ran = append(c.ran, line)
	for i, a := range args {
		if a == c.file && i+1 < len(args) {
			b, _ := os.ReadFile(args[i+1])
			if c.read == nil {
				c.read = map[string]string{}
			}
			c.read[line] = string(b)
		}
	}
	for prefix, err := range c.fail {
		if strings.HasPrefix(line, prefix) {
			return err
		}
	}
	return nil
}

var mine = service.Unit{Name: "tele", Binary: "/opt/homebrew/bin/tele", Config: "/Users/ada/.config/tele/config.yml"}

// A LaunchAgent is written where launchd reads the person's own, and loaded
// into their session; the log it names has a directory to go to (#182).
func TestLaunchd_InstallWritesTheAgentAndLoadsIt(t *testing.T) {
	home := t.TempDir()
	cmds := &commands{}
	m := service.Launchd{Home: home, UID: 501, Run: cmds.run}
	u := mine
	u.LogPath = filepath.Join(home, ".local", "state", "tele", "service.log")

	require.NoError(t, m.Install(u))

	plist := filepath.Join(home, "Library", "LaunchAgents", "io.github.sorokin-vladimir.tele.plist")
	written, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Equal(t, string(service.Plist(u)), string(written))
	assert.Equal(t, []string{"launchctl bootstrap gui/501 " + plist}, cmds.ran)
	assert.DirExists(t, filepath.Dir(u.LogPath))
}

// Installing again replaces the agent and starts it anew: the way to mend a
// unit whose command moved.
func TestLaunchd_InstallingAgainReplacesTheAgent(t *testing.T) {
	home := t.TempDir()
	m := service.Launchd{Home: home, UID: 501, Run: (&commands{}).run}
	require.NoError(t, m.Install(mine))
	cmds := &commands{fail: map[string]error{"launchctl bootout": errors.New("not loaded")}}
	m.Run = cmds.run

	require.NoError(t, m.Install(mine))

	plist := filepath.Join(home, "Library", "LaunchAgents", "io.github.sorokin-vladimir.tele.plist")
	assert.Equal(t, []string{
		"launchctl bootout gui/501/io.github.sorokin-vladimir.tele",
		"launchctl bootstrap gui/501 " + plist,
	}, cmds.ran)
}

// What launchd said when it would not load the agent is the error.
func TestLaunchd_ARefusalIsSaid(t *testing.T) {
	cmds := &commands{fail: map[string]error{"launchctl bootstrap": errors.New("Bootstrap failed: 5: Input/output error")}}
	m := service.Launchd{Home: t.TempDir(), UID: 501, Run: cmds.run}

	err := m.Install(mine)

	assert.ErrorContains(t, err, "Input/output error")
}

// Uninstalling stops the agent and removes it; with nothing installed there is
// nothing to do, and that is no failure.
func TestLaunchd_Uninstall(t *testing.T) {
	home := t.TempDir()
	m := service.Launchd{Home: home, UID: 501, Run: (&commands{}).run}
	require.NoError(t, m.Install(mine))
	cmds := &commands{}
	m.Run = cmds.run

	removed, err := m.Uninstall("tele")

	require.NoError(t, err)
	assert.True(t, removed)
	assert.Equal(t, []string{"launchctl bootout gui/501/io.github.sorokin-vladimir.tele"}, cmds.ran)
	assert.NoFileExists(t, filepath.Join(home, "Library", "LaunchAgents", "io.github.sorokin-vladimir.tele.plist"))

	removed, err = m.Uninstall("tele")
	require.NoError(t, err)
	assert.False(t, removed)
}

// A systemd user unit is written where the user's manager reads it, enabled
// for the next login, and started now - again, when it was running.
func TestSystemd_InstallWritesTheUnitAndStartsIt(t *testing.T) {
	config := t.TempDir()
	cmds := &commands{}
	m := service.Systemd{ConfigHome: config, Run: cmds.run}

	require.NoError(t, m.Install(mine))

	written, err := os.ReadFile(filepath.Join(config, "systemd", "user", "tele.service"))
	require.NoError(t, err)
	assert.Equal(t, string(service.SystemdUnit(mine)), string(written))
	assert.Equal(t, []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable tele.service",
		"systemctl --user restart tele.service",
	}, cmds.ran)
}

func TestSystemd_Uninstall(t *testing.T) {
	config := t.TempDir()
	m := service.Systemd{ConfigHome: config, Run: (&commands{}).run}
	require.NoError(t, m.Install(mine))
	cmds := &commands{}
	m.Run = cmds.run

	removed, err := m.Uninstall("tele")

	require.NoError(t, err)
	assert.True(t, removed)
	assert.Equal(t, []string{
		"systemctl --user disable --now tele.service",
		"systemctl --user daemon-reload",
	}, cmds.ran)
	assert.NoFileExists(t, filepath.Join(config, "systemd", "user", "tele.service"))

	removed, err = m.Uninstall("tele")
	require.NoError(t, err)
	assert.False(t, removed)
}

// A task is created from its XML, replacing one of the same name, and started
// now; one still running is ended first, so it starts on the new command.
func TestTaskScheduler_InstallCreatesTheTaskAndRunsIt(t *testing.T) {
	cmds := &commands{file: "/XML", fail: map[string]error{"schtasks /End": errors.New("not running")}}
	m := service.TaskScheduler{TempDir: t.TempDir(), Run: cmds.run}

	require.NoError(t, m.Install(mine))

	require.Len(t, cmds.ran, 3)
	assert.Equal(t, "schtasks /End /TN tele", cmds.ran[0])
	assert.True(t, strings.HasPrefix(cmds.ran[1], "schtasks /Create /TN tele /XML "), cmds.ran[1])
	assert.True(t, strings.HasSuffix(cmds.ran[1], " /F"), cmds.ran[1])
	assert.Equal(t, string(service.TaskXML(mine)), cmds.read[cmds.ran[1]], "the task is created from the unit")
	assert.Equal(t, "schtasks /Run /TN tele", cmds.ran[2])
}

// Uninstalling ends the task and deletes it; with no task there is nothing to
// do.
func TestTaskScheduler_Uninstall(t *testing.T) {
	cmds := &commands{}
	m := service.TaskScheduler{TempDir: t.TempDir(), Run: cmds.run}

	removed, err := m.Uninstall("tele")

	require.NoError(t, err)
	assert.True(t, removed)
	assert.Equal(t, []string{
		"schtasks /Query /TN tele",
		"schtasks /End /TN tele",
		"schtasks /Delete /TN tele /F",
	}, cmds.ran)

	none := &commands{fail: map[string]error{"schtasks /Query": errors.New("ERROR: The system cannot find the file specified.")}}
	m.Run = none.run
	removed, err = m.Uninstall("tele")
	require.NoError(t, err)
	assert.False(t, removed)
	assert.Equal(t, []string{"schtasks /Query /TN tele"}, none.ran)
}
