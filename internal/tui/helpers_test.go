package tui

import (
	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
)

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
		client:      &fakeGH{},
		views:       config.DefaultViews(),
		title:       "CodeDiff review mode",
		prList:      prListModel{view: allPRsView},
		diffCommand: "",
		base:        "main",
		diffBase:    "main",
		head:        "feature/x",
		events: []event.Event{
			{TS: "2026-07-21T10:00", Kind: event.Decision, Title: "chose Go", Body: "gh-dash stack"},
			{TS: "2026-07-21T11:00", Kind: event.Commit, Title: "feat: x", SHA: "abc1234"},
		},
		files:             []git.ChangedFile{{Status: "M", Path: "internal/tui/tui.go"}},
		commits:           []git.Commit{{SHA: "abc1234", Subject: "feat: x", Date: "2026-07-21T11:00"}},
		conversationDirty: true,
		help:              newHelp(),
		keys:              keys,
	}
}
