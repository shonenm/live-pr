package tui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/portalis"
)

// terminalSessionID recovers the terminal's private session ID through the
// exported Handles predicate so a test can address PTY messages to it.
func terminalSessionID(t *testing.T, terminal *embeddedterm.Terminal) string {
	t.Helper()
	for i := 1; i < 100_000; i++ {
		id := fmt.Sprintf("live-pr-diff-%d", i)
		if terminal.Handles(embeddedterm.StateMsg{SessionID: id}) {
			return id
		}
	}
	t.Fatal("embedded terminal session ID not found")
	return ""
}

func TestOverlayDoesNotSwallowEmbeddedTerminalMessages(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	defer m.close()
	if !m.diffTerminal.Available() {
		t.Skip("embedded PTY is not supported on this platform")
	}
	id := terminalSessionID(t, m.diffTerminal)
	m.overlay = helpOverlay{}

	u, _ := m.Update(portalis.PtyExitMsg{SessionID: id, Err: errors.New("nvim died")})
	m = u.(Model)
	if m.diffTerminal.Available() {
		t.Fatal("overlay dropped the PTY exit, so the review pane never falls back to the raw diff")
	}
	if m.overlay == nil {
		t.Fatal("PTY exit closed the overlay")
	}
}

func TestOverlayStillOwnsMessagesForOtherTerminals(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	defer m.close()
	if !m.diffTerminal.Available() {
		t.Skip("embedded PTY is not supported on this platform")
	}
	m.overlay = helpOverlay{}

	u, cmd := m.Update(portalis.PtyExitMsg{SessionID: "stale-session"})
	m = u.(Model)
	if cmd != nil || !m.diffTerminal.Available() {
		t.Fatalf("stale PTY message reached the terminal: cmd=%v available=%v", cmd, m.diffTerminal.Available())
	}
}

func TestOverlayKeepsCacheSaveErrorsVisible(t *testing.T) {
	for _, tc := range []struct {
		msg  tea.Msg
		want string
	}{
		{cacheSaved{err: errors.New("disk full")}, "GitHub cache: disk full"},
		{navigatorCacheSaved{err: errors.New("disk full")}, "PR list cache: disk full"},
	} {
		m := testModel()
		m.overlay = helpOverlay{}
		u, _ := m.Update(tc.msg)
		m = u.(Model)
		if m.status != tc.want {
			t.Errorf("%T under overlay: status=%q want %q", tc.msg, m.status, tc.want)
		}
	}
}
