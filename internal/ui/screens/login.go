package screens

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core/project"
)

type AuthRequestMsg struct {
	Step project.LoginStepKind
	Hint string
	// Err is why the step is asked again, shown under the field (#285).
	Err string
	// Seq names this asking of the step, for the answer to name back.
	Seq int
}

// LoginAnswerMsg is what was typed at a login step, for the root to hand to
// the owner. It names the asking it answers, so an answer to a step already
// answered elsewhere is refused rather than taken for the next (#309).
type LoginAnswerMsg struct {
	Seq   int
	Value string
}
type AuthErrorMsg struct{ Text string }
type ConnectedMsg struct{}
type TransitionToMainMsg struct{}

// SlowConnectMsg arrives SlowConnectAfter into the login screen. If Telegram
// has not answered by then, the screen says what is worth checking (#283).
type SlowConnectMsg struct{}

// SlowConnectAfter is how long a connection may take before the screen stops
// saying only "connecting...". Nothing is given up when it passes: gotd keeps
// reconnecting on its own, and a network that comes back or a clock set right
// while the screen is up lets the same attempt through. A constant rather than a setting, since all it
// changes is a line of text.
const SlowConnectAfter = 10 * time.Second

// SlowConnectTick schedules the SlowConnectMsg.
func SlowConnectTick() tea.Cmd {
	return tea.Tick(SlowConnectAfter, func(time.Time) tea.Msg { return SlowConnectMsg{} })
}

type LoginModel struct {
	input textinput.Model
	step  project.LoginStepKind
	// seq names the asking of the step on screen.
	seq    int
	prompt string
	err    string
	// notice says why the person is at the login, under the number the first
	// time it is asked for (#297).
	notice string
	// slow is set when SlowConnectMsg arrives while still connecting.
	slow bool
}

// SetNotice sets what is said under the number the first time it is asked for:
// why the person is back at the login.
func (m *LoginModel) SetNotice(text string) { m.notice = text }

func NewLoginModel() LoginModel {
	ti := textinput.New()
	ti.Focus()
	ti.SetWidth(40)
	// Paste is the terminal's own, arriving as a tea.PasteMsg (#284). The
	// input's ctrl+v would read the clipboard itself and answer with a message
	// of its own that never comes back to it through the root.
	ti.KeyMap.Paste = key.NewBinding()
	return LoginModel{
		input: ti,
		step:  -1,
	}
}

func (m LoginModel) CurrentStep() project.LoginStepKind { return m.step }

// Connecting reports whether Telegram has not answered yet: no prompt has been
// asked for and nothing has failed. The error step is negative too, which is why
// this is not a sign test on the step.
func (m LoginModel) Connecting() bool { return m.step == -1 }

// AskingNumber reports whether the phone number is the step on screen, which
// is also the step a failure just after it was entered arrived on.
func (m LoginModel) AskingNumber() bool { return m.step == project.LoginPhone }

// Slow reports whether the connection is still being waited for past
// SlowConnectAfter.
func (m LoginModel) Slow() bool { return m.slow && m.Connecting() }

func (m LoginModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m LoginModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case SlowConnectMsg:
		if m.Connecting() {
			m.slow = true
		}
		return m, nil

	case AuthRequestMsg:
		m.step = msg.Step
		m.seq = msg.Seq
		m.err = msg.Err
		if msg.Step == project.LoginPhone && m.notice != "" {
			// Said once: a number asked for again has a reason of its own.
			if m.err == "" {
				m.err = m.notice
			}
			m.notice = ""
		}
		switch msg.Step {
		case project.LoginPhone:
			m.prompt = "Enter phone number:"
			m.input.Placeholder = "+1234567890"
		case project.LoginCode:
			m.prompt = msg.Hint
			if m.prompt == "" {
				m.prompt = "Enter the login code:"
			}
			m.input.Placeholder = "12345"
		case project.LoginPassword:
			m.prompt = "Enter 2FA password:"
			m.input.EchoMode = textinput.EchoPassword
			m.input.Placeholder = "password"
		}
		m.input.SetValue("")
		return m, nil

	case AuthErrorMsg:
		m.err = msg.Text
		m.step = -2
		return m, nil

	case ConnectedMsg:
		return m, func() tea.Msg { return TransitionToMainMsg{} }

	case tea.KeyPressMsg:
		if msg.Code == tea.KeyEnter && m.step >= 0 {
			val := m.input.Value()
			// A copied number or code often brings a space or a line break
			// along, which Telegram would refuse. A password is sent as typed:
			// a space may be part of it, and Telegram trims nothing (#284).
			if m.step != project.LoginPassword {
				val = strings.TrimSpace(val)
			}
			answer := LoginAnswerMsg{Seq: m.seq, Value: val}
			m.input.Reset()
			return m, func() tea.Msg { return answer }
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m LoginModel) View() tea.View {
	var s string
	switch {
	case m.step == -2:
		// The text arrives complete, with the log and the quit key: which key
		// quits is the root's to know, not this screen's.
		s = m.err
	case m.step < 0:
		s = "Connecting...\n"
	default:
		s = fmt.Sprintf("%s\n\n%s\n\n%s", m.prompt, m.input.View(), m.err)
	}
	return tea.NewView(s)
}
