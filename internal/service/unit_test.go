package service_test

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/service"
)

// A config of its own gets a service of its own, so stable and beta, or a
// personal and a work account, never overwrite each other's unit (#182).
func TestName_OneServicePerConfig(t *testing.T) {
	assert.Equal(t, "tele", service.Name("tele", "/home/ada/.local/state/tele", true))
	assert.Equal(t, "tele-beta", service.Name("tele-beta", "/home/ada/.local/state/tele-beta", true))

	work := service.Name("tele", "/home/ada/work/state", false)
	assert.Regexp(t, `^tele-[0-9a-f]{8}$`, work)
	assert.Equal(t, work, service.Name("tele", "/home/ada/work/state", false), "the same state directory, the same name")
	assert.NotEqual(t, work, service.Name("tele", "/home/ada/other/state", false))
}

// unit is a service whose paths need care in every format: a space, and a
// character XML escapes.
var unit = service.Unit{
	Name:    "tele",
	Binary:  "/opt/homebrew/bin/tele",
	Config:  "/Users/ada/My Config/tele & co 100%.yml",
	LogPath: "/Users/ada/.local/state/tele/service.log",
}

// plistDict reads the top-level dict of a property list into its keys:
// strings, booleans, and arrays of strings.
func plistDict(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(string(doc)))
	dec.Strict = true
	got := map[string]any{}
	var key string
	var array []string
	inArray := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "key", "string":
				var text string
				require.NoError(t, dec.DecodeElement(&text, &el))
				switch {
				case el.Name.Local == "key":
					key = text
				case inArray:
					array = append(array, text)
				default:
					got[key] = text
				}
			case "true", "false":
				got[key] = el.Name.Local == "true"
			case "array":
				inArray, array = true, nil
			}
		case xml.EndElement:
			if el.Name.Local == "array" {
				got[key] = array
				inArray = false
			}
		}
	}
	return got
}

// A LaunchAgent the service manager keeps running: started at login and again
// whenever it exits, as a service, on the config it was installed with.
func TestPlist_KeepsTheServiceRunning(t *testing.T) {
	got := plistDict(t, service.Plist(unit))

	assert.Equal(t, service.Label(unit), got["Label"])
	assert.Equal(t, []string{"/opt/homebrew/bin/tele", "daemon", "--service", "--config", "/Users/ada/My Config/tele & co 100%.yml"},
		got["ProgramArguments"])
	assert.Equal(t, true, got["KeepAlive"])
	assert.Equal(t, true, got["RunAtLoad"])
	assert.Equal(t, unit.LogPath, got["StandardErrorPath"])
}

// A systemd user unit started again whenever it exits, with each argument
// quoted so a path with a space stays one argument.
func TestSystemdUnit_KeepsTheServiceRunning(t *testing.T) {
	lines := strings.Split(string(service.SystemdUnit(unit)), "\n")

	assert.Contains(t, lines, `ExecStart="/opt/homebrew/bin/tele" "daemon" "--service" "--config" "/Users/ada/My Config/tele & co 100%%.yml"`)
	assert.Contains(t, lines, "Restart=always")
	assert.Contains(t, lines, "WantedBy=default.target")
}

// task is what a Task Scheduler task says about how it runs.
type task struct {
	Triggers struct {
		Logon *struct{} `xml:"LogonTrigger"`
	} `xml:"Triggers"`
	Settings struct {
		MultipleInstances  string `xml:"MultipleInstancesPolicy"`
		ExecutionTimeLimit string `xml:"ExecutionTimeLimit"`
		StopOnBatteries    string `xml:"StopIfGoingOnBatteries"`
		NotOnBatteries     string `xml:"DisallowStartIfOnBatteries"`
		RestartOnFailure   *struct {
			Interval string `xml:"Interval"`
			Count    int    `xml:"Count"`
		} `xml:"RestartOnFailure"`
	} `xml:"Settings"`
	Actions struct {
		Exec struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

// A task started at logon and again whenever it fails, which a restart is,
// with no time limit: the default one ends a task after three days (#182).
func TestTaskXML_KeepsTheServiceRunning(t *testing.T) {
	win := service.Unit{Name: "tele", Binary: `C:\Users\ada\scoop\shims\tele.exe`, Config: `C:\Users\ada\My Config\config.yml`}
	var got task

	doc := service.TaskXML(win)
	require.Equal(t, []byte{0xFF, 0xFE}, doc[:2], "schtasks reads UTF-16 with a byte order mark")
	units := make([]uint16, 0, len(doc)/2)
	for i := 2; i+1 < len(doc); i += 2 {
		units = append(units, uint16(doc[i])|uint16(doc[i+1])<<8)
	}
	dec := xml.NewDecoder(strings.NewReader(string(utf16.Decode(units))))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	require.NoError(t, dec.Decode(&got))

	assert.NotNil(t, got.Triggers.Logon)
	assert.Equal(t, `C:\Users\ada\scoop\shims\tele.exe`, got.Actions.Exec.Command)
	assert.Equal(t, `daemon --service --config "C:\Users\ada\My Config\config.yml"`, got.Actions.Exec.Arguments)
	require.NotNil(t, got.Settings.RestartOnFailure)
	assert.Equal(t, "PT1M", got.Settings.RestartOnFailure.Interval)
	assert.Positive(t, got.Settings.RestartOnFailure.Count)
	assert.Equal(t, "PT0S", got.Settings.ExecutionTimeLimit)
	assert.Equal(t, "IgnoreNew", got.Settings.MultipleInstances)
	assert.Equal(t, "false", got.Settings.StopOnBatteries)
	assert.Equal(t, "false", got.Settings.NotOnBatteries)
}
