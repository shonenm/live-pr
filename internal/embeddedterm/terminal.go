//go:build !windows

// Package embeddedterm hosts an interactive command in a PTY-backed terminal.
package embeddedterm

import (
	"fmt"
	"strings"

	portalis "github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
)

// Terminal embeds one interactive process and its VT screen.
type Terminal struct {
	sessionID string
	emulator  *portalis.Emulator
	started   bool
	exited    bool
	closed    bool
	err       error
}

// New prepares command for execution in cwd. Environment values are appended
// to the inherited process environment by the PTY implementation.
func New(command, cwd string, env []string) *Terminal {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	// exec ensures closing live-pr terminates the actual reviewer, not only sh.
	sessionID := nextSessionID()
	emulator := portalis.NewEmulator(sessionID, "CodeReview", "sh", []string{"-c", "exec " + command})
	emulator.SetInitialCWD(cwd)
	emulator.SetStartEnv(env)
	emulator.SetScrollbackLimit(2_000)
	return &Terminal{sessionID: sessionID, emulator: emulator}
}

// Init starts the child before returning its first output listener. Synchronous
// spawn prevents a late-starting reviewer from escaping shutdown ownership.
func (t *Terminal) Init() tea.Cmd {
	if t == nil || t.closed {
		return nil
	}
	if err := t.emulator.StartSync(nil); err != nil {
		t.exited = true
		t.err = fmt.Errorf("CodeReview: %w", err)
		return func() tea.Msg { return StateMsg{SessionID: t.sessionID} }
	}
	t.started = true
	return t.emulator.Listen()
}

// Handles reports whether msg belongs to the embedded PTY lifecycle.
func (t *Terminal) Handles(msg tea.Msg) bool {
	if t == nil {
		return false
	}
	switch msg := msg.(type) {
	case StateMsg:
		return msg.SessionID == t.sessionID
	case portalis.PtyReadyMsg:
		return msg.SessionID == t.sessionID
	case portalis.PtyOutputMsg:
		return msg.SessionID == t.sessionID
	case portalis.PtyExitMsg:
		return msg.SessionID == t.sessionID
	case portalis.RenderTickMsg:
		return msg.SessionID == t.sessionID
	}
	return false
}

// Update advances the PTY/emulator lifecycle or forwards focused input.
func (t *Terminal) Update(msg tea.Msg) tea.Cmd {
	if t == nil {
		return nil
	}
	switch msg := msg.(type) {
	case StateMsg:
		return nil
	case portalis.PtyReadyMsg:
		if msg.SessionID == t.sessionID {
			t.started = true
		}
	case portalis.PtyExitMsg:
		if msg.SessionID == t.sessionID {
			t.exited = true
			if msg.Err != nil {
				t.err = fmt.Errorf("CodeReview: %w", msg.Err)
			}
			t.emulator.Close()
			t.closed = true
			return nil
		}
	}
	return t.emulator.Update(msg)
}

// Resize updates both the virtual screen and its PTY.
func (t *Terminal) Resize(width, height int) {
	if t == nil || width < 2 || height < 2 {
		return
	}
	t.emulator.Update(portalis.ResizeMsg{Width: width, Height: height})
}

// View returns exactly the current virtual terminal screen.
func (t *Terminal) View(width, height int) string {
	if t == nil {
		return ""
	}
	if !t.started && t.err == nil {
		return "Starting CodeReview…"
	}
	return t.emulator.View(width, height)
}

// Available reports whether the embedded reviewer can receive input.
func (t *Terminal) Available() bool { return t != nil && !t.exited && t.err == nil }

// Err returns a startup/runtime error, if any.
func (t *Terminal) Err() error {
	if t == nil {
		return nil
	}
	return t.err
}

// Close terminates and reaps the child process.
func (t *Terminal) Close() {
	if t == nil || t.closed {
		return
	}
	t.closed = true
	t.exited = true
	if t.emulator != nil {
		t.emulator.Close()
	}
}
