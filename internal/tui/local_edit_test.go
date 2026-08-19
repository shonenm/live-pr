package tui

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/event"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestLocalPRCommentsCanBeManagedFromTUI(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	st := store.ForBranch(root, "feature")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.root, m.currentBranch, m.timelinePath = root, "feature", st.Timeline()

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = u.(Model)
	if o, ok := m.overlay.(localEditOverlay); !ok || o.mode != addLocalComment {
		t.Fatalf("a did not open comment editor: %#v", m.overlay)
	}
	if background := m.localEditor.FocusedStyle.CursorLine.GetBackground(); background != (lipgloss.NoColor{}) {
		t.Fatalf("comment editor cursor line background = %v, want transparent", background)
	}
	m.localEditor.SetValue("kind: decision\n\nKeep append-only history\n\nAvoid lost concurrent comments.")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)
	events, err := event.Load(st.Timeline())
	if err != nil || len(events) != 1 || events[0].Author != "user" || events[0].Title != "Keep append-only history" {
		t.Fatalf("TUI add = %+v, %v", events, err)
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = u.(Model)
	m.localEditor.SetValue("kind: pivot\n\nUse operation records\n\nPreserve the original decision too.")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)
	events, _ = event.Load(st.Timeline())
	if len(events) != 1 || events[0].Kind != event.Pivot || events[0].UpdatedAt == "" {
		t.Fatalf("TUI edit = %+v", events)
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = u.(Model)
	if o, ok := m.overlay.(localDeleteOverlay); !ok || o.target == "" {
		t.Fatal("d did not request comment deletion")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = u.(Model)
	events, _ = event.Load(st.Timeline())
	if len(events) != 0 || m.screen != detailScreen {
		t.Fatalf("TUI delete = %+v screen=%v", events, m.screen)
	}
}

func TestLocalPRSummaryCanBeEditedFromTUI(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	st := store.ForBranch(root, "feature")
	if err := st.WriteConclusion("# Initial\n"); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.root, m.currentBranch, m.timelinePath, m.summary = root, "feature", st.Timeline(), "# Initial\n"
	m.invalidateConversation()

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = u.(Model)
	if o, ok := m.overlay.(localEditOverlay); !ok || o.mode != editLocalSummary {
		t.Fatalf("e did not open summary editor: %#v", m.overlay)
	}
	m.localEditor.SetValue("# Final outcome\n\n## Summary\n\nImplemented result.")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)
	body, err := os.ReadFile(st.Conclusion())
	if err != nil || !strings.Contains(string(body), "Implemented result") || m.title != "Final outcome" {
		t.Fatalf("TUI summary = %q title=%q err=%v", body, m.title, err)
	}
}

func TestRemoteDeleteTitleTruncatesOnRuneBoundary(t *testing.T) {
	m := testModel()
	m.viewerLogin = "me"
	body := strings.Repeat("あ", 40)
	comment := gh.Comment{ID: 5, Body: body}
	comment.User.Login = "me"
	m.cache.Comments = []gh.Comment{comment}
	m.conversationDirty = true
	items := m.conversationItems()
	for i, it := range items {
		if it.comment != nil {
			m.cursors[conversationTab] = i
		}
	}
	next, _ := m.deleteSelectedLocalComment()
	o, ok := next.overlay.(remoteDeleteOverlay)
	if !ok {
		t.Fatalf("d did not open the remote delete confirm: %#v", next.overlay)
	}
	title := o.title
	if !utf8.ValidString(title) {
		t.Fatalf("truncation split a rune: %q", title)
	}
	if !strings.HasSuffix(title, "…") || strings.Contains(title, "�") {
		t.Fatalf("title = %q", title)
	}
}

// Shift+Enter reaches the program as CR on most terminals and LF (ctrl+j) on
// others; both must insert a newline. The editor also has to fit the popup
// that renders it, or the popup re-wraps its lines and shows breaks the text
// does not contain.
func TestEditorNewlineKeysAndWidthFitPopup(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyCtrlJ},
		{Type: tea.KeyRunes, Runes: []rune("\r"), Alt: true},
	} {
		m := testModel()
		m.w, m.h = 120, 40
		m.cache.PR = &gh.PR{Number: 1, URL: "u"}
		next, _ := m.openLocalEditor(localEditOverlay{mode: addRemoteComment}, "line")
		next.localEditor.SetValue("line")
		u, _ := next.Update(key)
		if got := u.(Model).localEditor.Value(); !strings.Contains(got, "\n") {
			t.Fatalf("%v did not insert a newline: %q", key, got)
		}
	}

	// The review popup declares its width; the editor must stay inside it.
	for _, w := range []int{60, 100, 160} {
		m := testModel()
		m.w, m.h = w, 40
		m.cache.PR = &gh.PR{Number: 1, HeadRefOID: "abc"}
		m.reviewDraft = gh.NewReviewDraft(1, "abc")
		next, _ := m.setupLocalEditor(strings.Repeat("x", 300))
		popup := reviewSubmitOverlay{event: gh.ReviewCommentEvent, typing: true}
		next.overlay = popup
		budget := max(36, min(80, w-14)) - 4
		if got := next.localEditor.Width(); got > budget {
			t.Fatalf("w=%d editor width %d exceeds the popup budget %d", w, got, budget)
		}
		// The popup must not need to wrap anything the editor rendered.
		for _, line := range strings.Split(ansi.Strip(popup.render(next)), "\n") {
			if lipgloss.Width(line) > budget+6 {
				t.Fatalf("w=%d popup line %q exceeds its own frame", w, line)
			}
		}
	}
}

func TestEditorAdvertisesTheKeyThatActuallySaves(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, URL: "u"}

	// e / a open the overlay; the hint must name a key bubbletea can report.
	next, _ := m.openLocalEditor(localEditOverlay{mode: addRemoteComment}, "")
	hint := ansi.Strip(next.overlay.render(next))
	if !strings.Contains(hint, "Ctrl+S send") {
		t.Fatalf("editor hint = %q", hint)
	}

	// Enter stays a newline; Ctrl+S sends.
	next.localEditor.SetValue("hello")
	typed, _ := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := typed.(Model); !strings.Contains(got.localEditor.Value(), "\n") {
		t.Fatalf("enter did not insert a newline: value=%q", got.localEditor.Value())
	} else if o, ok := got.overlay.(localEditOverlay); !ok || o.mode != addRemoteComment {
		t.Fatalf("enter closed or replaced the editor overlay: %#v", got.overlay)
	}
	sent, cmd := next.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if got := sent.(Model); got.overlay != nil || cmd == nil {
		t.Fatalf("ctrl+s did not send: overlay=%#v cmd=%v", got.overlay, cmd)
	}
}
