package wire

import "encoding/json"

// The calls about the host rather than about an account. They are the
// protocol's, like hello, and are answered whether or not an owner is seated,
// on the login screen and after a log out alike: the config file is the
// host's, and it outlives the accounts that pass through it (#315).
const (
	// MethodSettings asks for the host's settings as it holds them.
	MethodSettings = "$settings"
	// MethodSet changes one host setting. The host checks it, writes it into
	// its own config file and reloads: a window never writes a host setting
	// itself.
	MethodSet = "$set"
	// MethodReload asks the host to read its config file again.
	MethodReload = "$reload"
)

// Warning is something wrong with one of the host's settings, as a client
// shows it. One with an ID is shown only until it has been seen.
type Warning struct {
	Text string `json:"text"`
	ID   string `json:"id,omitempty"`
}

// HostSettings is what the host answers MethodSettings with.
type HostSettings struct {
	// Config is the file the host reads its settings from.
	Config   string    `json:"config"`
	Settings []Setting `json:"settings"`
}

// Setting is one host setting as the host holds it.
type Setting struct {
	Key string `json:"key"`
	// Value is what the setting is worth, encoded as JSON. Which Go type it
	// decodes into is the setting's, which the client knows from the key.
	Value json.RawMessage `json:"value,omitempty"`
	// Described is said in place of a value the host does not show, such as
	// which built-in app key is in force when the file names none.
	Described string `json:"described,omitempty"`
	// Default says the file does not name the setting.
	Default bool `json:"default,omitempty"`
}

// SetParams is what MethodSet carries.
type SetParams struct {
	Key string `json:"key"`
	// Value is the new value encoded as JSON. Nil resets the setting to its
	// default: absence is what a default is.
	Value json.RawMessage `json:"value,omitempty"`
}

// SetResult is what the host said of a change. A refusal is an answer, not a
// failure of the call: the host understood and would not write it.
type SetResult struct {
	// Refused says why the value was not written, in words for the row it is
	// about. Empty when it was.
	Refused  string    `json:"refused,omitempty"`
	Warnings []Warning `json:"warnings,omitempty"`
}

// ReloadResult is what the host found when it read its file again.
type ReloadResult struct {
	// Failed says why the file was not taken, in which case the host goes on
	// with the settings it had. Empty when it was.
	Failed   string    `json:"failed,omitempty"`
	Warnings []Warning `json:"warnings,omitempty"`
}
