package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/event"
)

func testModel() Model {
	return Model{
		title: "CodeDiff review mode",
		base:  "main",
		head:  "feature/x",
		events: []event.Event{
			{TS: "2026-07-21T10:00", Kind: event.Decision, Title: "chose Go", Body: "gh-dash stack"},
			{TS: "2026-07-21T11:00", Kind: event.Commit, Title: "feat: x", SHA: "abc1234"},
		},
		help: help.New(),
		keys: keys,
	}
}

func TestViewRendersHeaderAndTimeline(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	out := m.View()
	for _, want := range []string{"Open", "main", "feature/x", "DECISION", "chose Go", "Conversation"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestCursorMovesAndPreviewSwitches(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.cursor)
	}
	// j → move to the commit event; must not panic and cursor advances.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor should be 1 after j, got %d", m.cursor)
	}
	if !strings.Contains(m.View(), "COMMIT") {
		t.Errorf("preview/list should show the COMMIT event after moving down")
	}
	// cannot move past the end
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	if m.cursor != 1 {
		t.Errorf("cursor should clamp at last event, got %d", m.cursor)
	}
}

func TestEnterLaunchesReviewerOnCommitOnly(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	// cursor 0 = decision (non-commit): no reviewer, a status hint instead.
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd != nil {
		t.Errorf("enter on a non-commit event must not launch the reviewer")
	}
	if m.status == "" {
		t.Errorf("expected a status hint when entering on a non-commit event")
	}

	// move to the commit event, enter → a reviewer command is returned.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Errorf("enter on a commit event must return a reviewer command")
	}
}
