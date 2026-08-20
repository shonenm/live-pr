package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
)

// keyPress builds a Bubble Tea v2 key-press message from a readable name:
// a single rune ("a", "G", "?"), a named key ("enter", "esc", "tab",
// "shift+tab", "space", "up", "down"), or "ctrl+<letter>". Multi-rune input
// becomes a single text event, which the filter editor consumes via Text.
func keyPress(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	if rest, ok := strings.CutPrefix(name, "ctrl+"); ok && len([]rune(rest)) == 1 {
		return tea.KeyPressMsg{Code: []rune(rest)[0], Mod: tea.ModCtrl}
	}
	runes := []rune(name)
	if len(runes) == 1 {
		return tea.KeyPressMsg{Code: runes[0], Text: name}
	}
	return tea.KeyPressMsg{Code: tea.KeyExtended, Text: name}
}

// The default view order, named for readability in tests. Production code
// indexes Model.views, which config supplies.
const (
	assignedView prView = iota
	reviewRequestedView
	allPRsView
	authoredView
	needsMeView
	closedPRsView
	prViewCount
)

// buildPRList is a test-only view of the list rows; production code uses
// buildPRListRows for the selected line as well.
func (m *Model) buildPRList() string {
	content, _ := m.buildPRListRows()
	return content
}

func testModel() Model {
	return Model{
		client:       &fakeGH{},
		checkoutHead: func(string) error { return nil },
		views:        config.DefaultViews(),
		prList:       prListModel{view: allPRsView},
		diffCommand:  "",
		detailView: detailModel{
			title:    "CodeDiff review mode",
			base:     "main",
			diffBase: "main",
			head:     "feature/x",
			events: []event.Event{
				{TS: "2026-07-21T10:00", Kind: event.Decision, Title: "chose Go", Body: "gh-dash stack"},
				{TS: "2026-07-21T11:00", Kind: event.Commit, Title: "feat: x", SHA: "abc1234"},
			},
			files:             []git.ChangedFile{{Status: "M", Path: "internal/tui/tui.go"}},
			commits:           []git.Commit{{SHA: "abc1234", Subject: "feat: x", Date: "2026-07-21T11:00"}},
			conversationDirty: true,
		},
		help: newHelp(),
		keys: keys,
	}
}
