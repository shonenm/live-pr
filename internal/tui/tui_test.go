package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
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
		files:   []git.ChangedFile{{Status: "M", Path: "internal/tui/tui.go"}},
		commits: []git.Commit{{SHA: "abc1234", Subject: "feat: x", Date: "2026-07-21T11:00"}},
		help:    help.New(),
		keys:    keys,
	}
}

func TestViewRendersHeaderAndTimeline(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	out := m.View()
	for _, want := range []string{"Local", "main", "feature/x", "decision", "chose Go", "Conversation"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestCursorMovesAndPreviewSwitches(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	if m.cursors[conversationTab] != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.cursors[conversationTab])
	}
	// j → move to the commit event; must not panic and cursor advances.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	if m.cursors[conversationTab] != 1 {
		t.Fatalf("cursor should be 1 after j, got %d", m.cursors[conversationTab])
	}
	if !strings.Contains(m.View(), "commit") {
		t.Errorf("timeline should show the commit event after moving down")
	}
	// cannot move past the end
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	if m.cursors[conversationTab] != 1 {
		t.Errorf("cursor should clamp at last event, got %d", m.cursors[conversationTab])
	}
}

func TestTabsSwitchAndKeepIndependentCursors(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = u.(Model)
	if m.active != filesTab || !strings.Contains(m.View(), "internal/tui/tui.go") {
		t.Fatalf("tab should switch to changed files")
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	m = u.(Model)
	if m.active != commitsTab || !strings.Contains(m.View(), "feat: x") {
		t.Fatalf("3 should switch to commits")
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = u.(Model)
	if m.active != conversationTab || m.cursors[conversationTab] != 1 {
		t.Fatalf("conversation cursor should be preserved")
	}
}

func TestGitHubRefreshIsExplicitAfterStartup(t *testing.T) {
	m := testModel()
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	m.cache = gh.NewCache(m.head)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	m.cache.LastPublishedManagedBodyHash = "published-hash"
	m.refreshing = false
	if m.Init() == nil {
		t.Fatal("opening the TUI should schedule one background refresh")
	}

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = u.(Model)
	if cmd == nil || !m.refreshing {
		t.Fatal("r should start one GitHub refresh")
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); duplicate != nil {
		t.Fatal("r must not start a second in-flight refresh")
	}

	u, _ = m.Update(githubRefreshed{pr: gh.PR{Number: 9, State: "OPEN", Body: "remotely edited"}})
	m = u.(Model)
	if m.refreshing || m.cache.PR == nil || m.cache.PR.Number != 9 {
		t.Fatalf("refresh result not cached: %#v", m.cache)
	}
	if m.cache.LastPublishedManagedBodyHash != "published-hash" {
		t.Fatal("refresh must not move the last-published conflict baseline")
	}
	if !strings.Contains(m.View(), "#9 open") {
		t.Fatal("cached PR metadata should be visible in the header")
	}

	u, _ = m.Update(githubRefreshed{err: gh.ErrPRNotFound})
	m = u.(Model)
	if m.cache.LastPublishedManagedBodyHash != "published-hash" {
		t.Fatal("a pull-only refresh must preserve the publish baseline when no open PR is found")
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

	// The Commits tab reuses the same reviewer action.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	m = u.(Model)
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Errorf("enter in the Commits tab must return a reviewer command")
	}
}
