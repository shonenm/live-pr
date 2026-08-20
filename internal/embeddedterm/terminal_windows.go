//go:build windows

package embeddedterm

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
)

var errUnsupported = errors.New("embedded CodeDiff is not supported on Windows")

// Terminal is the Windows fallback; static Git diff remains available.
type Terminal struct {
	sessionID string
	err       error
}

func New(command, _ string, _ []string) *Terminal {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return &Terminal{sessionID: nextSessionID(), err: errUnsupported}
}

func (t *Terminal) Init() tea.Cmd {
	if t == nil {
		return nil
	}
	return func() tea.Msg { return StateMsg{SessionID: t.sessionID} }
}

func (t *Terminal) Handles(msg tea.Msg) bool {
	state, ok := msg.(StateMsg)
	return t != nil && ok && state.SessionID == t.sessionID
}

func (t *Terminal) Update(tea.Msg) tea.Cmd { return nil }
func (t *Terminal) Resize(_, _ int)        {}
func (t *Terminal) View(_, _ int) string   { return "" }
func (t *Terminal) Available() bool        { return false }
func (t *Terminal) Err() error {
	if t == nil {
		return nil
	}
	return t.err
}
func (t *Terminal) Close() {}
