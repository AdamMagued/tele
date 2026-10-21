// Package service makes tele a service: a host the operating system's service
// manager starts at login and again whenever it exits (#182). A unit is what
// that manager is told to run; this file says it in each manager's own format,
// and nothing here touches the system.
package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Unit is a service as its service manager is to run it.
type Unit struct {
	// Name tells one service from another: one per config file.
	Name string
	// Binary is the stable entry point the service runs: the name on the PATH,
	// a symlink kept as it is, so that an upgrade replaces what it starts.
	Binary string
	// Config is the absolute path of the config file the service reads.
	Config string
	// LogPath is where the service's own output goes, where the manager does
	// not keep it itself.
	LogPath string
}

// Args is the command the service manager runs.
func (u Unit) Args() []string {
	return []string{u.Binary, "daemon", "--service", "--config", u.Config}
}

// Name is the name of the service of the build named app over stateDir. The
// default config's service is named after the build alone; any other's also
// carries a short hash of its state directory, so two configs never share a
// unit.
func Name(app, stateDir string, defaultConfig bool) string {
	if defaultConfig {
		return app
	}
	sum := sha256.Sum256([]byte(stateDir))
	return app + "-" + hex.EncodeToString(sum[:4])
}

// Label is the unit's name as launchd knows it.
func Label(u Unit) string { return "io.github.sorokin-vladimir." + u.Name }

// Plist is the unit as a LaunchAgent: started at login, and again whenever it
// exits.
func Plist(u Unit) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	plistString(&b, "Label", Label(u))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range u.Args() {
		b.WriteString("\t\t<string>" + escapeXML(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	if u.LogPath != "" {
		plistString(&b, "StandardErrorPath", u.LogPath)
		plistString(&b, "StandardOutPath", u.LogPath)
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

func plistString(b *bytes.Buffer, key, value string) {
	b.WriteString("\t<key>" + escapeXML(key) + "</key>\n\t<string>" + escapeXML(value) + "</string>\n")
}

// SystemdUnit is the unit as a systemd user unit, started again whenever it
// exits. Without linger, which is neither enabled nor offered, it lives while
// the person is logged in, as a service does on every platform (#182).
func SystemdUnit(u Unit) []byte {
	quoted := make([]string, 0, len(u.Args()))
	for _, arg := range u.Args() {
		quoted = append(quoted, systemdQuote(arg))
	}
	return []byte(strings.Join([]string{
		"[Unit]",
		"Description=tele, the Telegram connection with no window",
		"",
		"[Service]",
		"ExecStart=" + strings.Join(quoted, " "),
		"Restart=always",
		"RestartSec=1",
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n"))
}

// systemdQuote quotes s as one argument of ExecStart, where a specifier
// starts with % and a variable with $.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + r.Replace(s) + `"`
}

// TaskXML is the unit as a Task Scheduler task: started at logon, and again
// whenever it fails, which a restart is, since a service exits as a failure to
// be started again (#182). Task Scheduler waits a minute before it does. It is
// the file schtasks reads: UTF-16, little-endian, with a byte order mark.
func TaskXML(u Unit) []byte {
	units := utf16.Encode([]rune(taskXML(u)))
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xFF, 0xFE)
	for _, c := range units {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func taskXML(u Unit) string {
	args := u.Args()[1:]
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, windowsQuote(arg))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>tele, the Telegram connection with no window</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, escapeXML(u.Binary), escapeXML(strings.Join(quoted, " ")))
}

// windowsQuote quotes s as one argument the way Windows programs split their
// command line: quoted when it holds a space, a tab or a quote, with the
// backslashes before a quote doubled.
func windowsQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, c := range s {
		switch c {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteRune(c)
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')
	return b.String()
}

func escapeXML(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
