// Real-cursor placement: when a text input is focused, View must put the
// terminal cursor on the caret cell so IME preedit text composes in place.
package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/event"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestFilterCursorSitsAfterTheQuery(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	if m.View().Cursor != nil {
		t.Fatal("cursor visible without a focused input")
	}
	u, _ = m.Update(keyPress("/"))
	m = u.(Model)
	// A wide rune must advance the cursor by two cells, so the composition
	// window tracks multibyte queries too.
	for _, key := range []string{"a", "あ"} {
		u, _ = m.Update(keyPress(key))
		m = u.(Model)
	}
	cursor := m.View().Cursor
	if cursor == nil {
		t.Fatal("filter editing must show the real cursor")
	}
	rows := strings.Split(ansi.Strip(m.viewContent()), "\n")
	if cursor.Y >= len(rows) {
		t.Fatalf("cursor row %d beyond the frame (%d rows)", cursor.Y, len(rows))
	}
	before := ansi.Cut(rows[cursor.Y], 0, cursor.X)
	if !strings.HasSuffix(before, "aあ") {
		t.Fatalf("cursor at (%d,%d) does not follow the query: %q", cursor.X, cursor.Y, before)
	}

	u, _ = m.Update(keyPress("esc"))
	m = u.(Model)
	if m.View().Cursor != nil {
		t.Fatal("cursor still visible after leaving filter editing")
	}
}

func TestEditorOverlayCursorTracksTheCaret(t *testing.T) {
	root := t.TempDir()
	st := store.ForBranch(root, "feature")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.root, m.currentBranch, m.timelinePath = root, "feature", st.Timeline()
	m.detailView.events = []event.Event{{TS: "2026-07-21T10:00", Kind: event.Decision, Title: "chose Go"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = u.(Model)
	u, _ = m.Update(keyPress("a"))
	m = u.(Model)
	if _, ok := m.overlay.(localEditOverlay); !ok {
		t.Fatalf("a did not open the editor overlay: %#v", m.overlay)
	}
	cursor := m.View().Cursor
	if cursor == nil {
		t.Fatal("editor overlay must show the real cursor")
	}
	u, _ = m.Update(keyPress("x"))
	m = u.(Model)
	moved := m.View().Cursor
	if moved == nil || moved.X != cursor.X+1 || moved.Y != cursor.Y {
		t.Fatalf("typing moved the cursor from (%d,%d) to %+v, want one cell right", cursor.X, cursor.Y, moved)
	}
	u, _ = m.Update(keyPress("enter"))
	m = u.(Model)
	wrapped := m.View().Cursor
	if wrapped == nil || wrapped.Y != cursor.Y+1 {
		t.Fatalf("newline moved the cursor to %+v, want the next row", wrapped)
	}
	u, _ = m.Update(keyPress("esc"))
	m = u.(Model)
	if m.View().Cursor != nil {
		t.Fatal("cursor still visible after closing the editor")
	}
}

func TestReviewSubmitCursorAppearsOnlyWhileTyping(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = u.(Model)
	u, _ = m.Update(keyPress("v"))
	m = u.(Model)
	if _, ok := m.overlay.(reviewSubmitOverlay); !ok {
		t.Fatalf("v did not open the review submit overlay: %#v", m.overlay)
	}
	if m.View().Cursor != nil {
		t.Fatal("verdict picker must not show a cursor")
	}
	u, _ = m.Update(keyPress("enter"))
	m = u.(Model)
	cursor := m.View().Cursor
	if o, ok := m.overlay.(reviewSubmitOverlay); !ok || !o.typing || cursor == nil {
		t.Fatalf("typing mode without a cursor: overlay=%#v cursor=%v", m.overlay, cursor)
	}
	// The cursor must land inside the popup, on the editor's first cell row.
	rows := strings.Split(m.viewContent(), "\n")
	if cursor.Y <= 0 || cursor.Y >= len(rows) || cursor.X <= 0 {
		t.Fatalf("cursor (%d,%d) outside the frame", cursor.X, cursor.Y)
	}
}
