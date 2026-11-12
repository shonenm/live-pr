package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/v2/internal/git"
)

// j/k on the detail screen move the active tab's cursor, or the file cursor
// while the explorer has focus, and stop at both ends.
func TestDetailCursorKeysClampAtBothEnds(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.detailView.active = commitsTab
	m.detailView.commits = []git.Commit{{SHA: "a"}, {SHA: "b"}}
	m.detailView.files = []git.ChangedFile{{Status: "M", Path: "a.go"}, {Status: "M", Path: "b.go"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = u.(Model)

	press := func(keys ...string) {
		t.Helper()
		for _, k := range keys {
			u, _ := m.Update(keyPress(k))
			m = u.(Model)
		}
	}
	press("k")
	if got := m.detailView.cursors[commitsTab]; got != 0 {
		t.Fatalf("k at the top moved the commit cursor to %d", got)
	}
	press("j", "j", "j")
	if got := m.detailView.cursors[commitsTab]; got != 1 {
		t.Fatalf("j past the end left the commit cursor at %d, want 1", got)
	}

	m.detailView.focus = focusExplorer
	press("j", "j", "j")
	if m.detailView.fileCursor != 1 || m.detailView.cursors[commitsTab] != 1 {
		t.Fatalf("explorer j = file:%d commit:%d, want 1 and an untouched commit cursor", m.detailView.fileCursor, m.detailView.cursors[commitsTab])
	}
	press("k", "k", "k")
	if m.detailView.fileCursor != 0 {
		t.Fatalf("explorer k past the top left the file cursor at %d", m.detailView.fileCursor)
	}
}
