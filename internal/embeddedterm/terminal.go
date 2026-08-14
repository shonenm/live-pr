//go:build !windows

// Package embeddedterm hosts an interactive command in a PTY-backed terminal.
package embeddedterm

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	portalis "github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
)

// Terminal embeds one interactive process and its VT screen.
type Terminal struct {
	sessionID string
	pidFile   string
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
	// The child copy of live-pr watches this process and owns the reviewer group.
	// This survives parent SIGKILL, where deferred PTY cleanup cannot run.
	sessionID := nextSessionID()
	executable, err := os.Executable()
	if err != nil {
		return &Terminal{sessionID: sessionID, err: fmt.Errorf("CodeDiff watchdog: %w", err), exited: true}
	}
	pidFile := fmt.Sprintf("%s/live-pr-review-%d-%s.pid", os.TempDir(), os.Getpid(), sessionID)
	emulator := portalis.NewEmulator(sessionID, "CodeDiff", executable, nil)
	emulator.SetInitialCWD(cwd)
	env = append(env,
		watchdogModeEnv+"=1",
		watchdogParentEnv+"="+strconv.Itoa(os.Getpid()),
		watchdogCommandEnv+"="+command,
		watchdogPIDFileEnv+"="+pidFile,
	)
	emulator.SetStartEnv(env)
	emulator.SetScrollbackLimit(2_000)
	return &Terminal{sessionID: sessionID, pidFile: pidFile, emulator: emulator}
}

// Init starts the child before returning its first output listener. Synchronous
// spawn prevents a late-starting reviewer from escaping shutdown ownership.
func (t *Terminal) Init() tea.Cmd {
	if t == nil || t.closed {
		return nil
	}
	if err := t.emulator.StartSync(nil); err != nil {
		t.exited = true
		t.err = fmt.Errorf("CodeDiff: %w", err)
		return func() tea.Msg { return StateMsg{SessionID: t.sessionID} }
	}
	t.started = true
	// Portalis keeps cursor blinking as a host-level concern. live-pr has one
	// embedded terminal, so make the cursor visible without waiting for a
	// global blink broadcaster.
	t.emulator.Update(portalis.CursorBlinkMsg{})
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
				t.err = fmt.Errorf("CodeDiff: %w", msg.Err)
			}
			t.emulator.Close()
			t.closed = true
			return nil
		}
	}
	cmd := t.emulator.Update(msg)
	if output, ok := msg.(portalis.PtyOutputMsg); ok && output.SessionID == t.sessionID {
		t.drainOutput()
	}
	return cmd
}

// drainOutput applies a small burst of already-buffered PTY output before the
// next Bubble Tea frame. Neovim redraws commonly span several 4 KiB reads;
// coalescing those reads avoids rendering the whole terminal once per chunk.
func (t *Terminal) drainOutput() {
	pty := t.emulator.Pty()
	if pty == nil {
		return
	}
	for range 8 {
		select {
		case data := <-pty.Output:
			if len(data) == 0 {
				return
			}
			t.emulator.Update(portalis.PtyOutputMsg{SessionID: t.sessionID, Data: data})
		default:
			return
		}
	}
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
		return "Starting CodeDiff…"
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
	if data, err := os.ReadFile(t.pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			killTree(pid)
		}
		_ = os.Remove(t.pidFile)
	}
	if t.emulator != nil {
		t.emulator.Close()
	}
}
