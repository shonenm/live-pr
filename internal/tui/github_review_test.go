package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestParseInlineReviewComment(t *testing.T) {
	comment, err := parseInlineReviewComment("path: internal/x.go\nline: 14\nside: RIGHT\n\nHandle the error.")
	if err != nil || comment.Path != "internal/x.go" || comment.Line != 14 || comment.Side != "RIGHT" || comment.Body != "Handle the error." {
		t.Fatalf("comment = %#v err=%v", comment, err)
	}
	if _, err := parseInlineReviewComment("path: x.go\nline: nope\nside: RIGHT\n\nbody"); err == nil {
		t.Fatal("invalid line accepted")
	}
}

func TestTUIAddsGeneralAndInlineReviewDraft(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	m.files = []git.ChangedFile{{Path: "main.go", Status: "M"}}
	m.diffCommand, m.diffTerminal = "", nil

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = u.(Model)
	if m.localEditMode != editReviewBody {
		t.Fatalf("general review editor = %v", m.localEditMode)
	}
	m.localEditor.SetValue("Overall review body")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)

	m.focusExplorer = true
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = u.(Model)
	if m.localEditMode != addInlineReviewComment || !strings.Contains(m.localEditor.Value(), "path: main.go") {
		t.Fatalf("inline review editor = %v value=%q", m.localEditMode, m.localEditor.Value())
	}
	m.localEditor.SetValue("path: main.go\nline: 3\nside: RIGHT\n\nFix this.")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)

	path := store.PullRequestReviewDraft(m.root, 12)
	draft, err := gh.LoadReviewDraft(path, 12, "abc123")
	if err != nil || draft.Body != "Overall review body" || len(draft.Comments) != 1 || draft.Comments[0].Line != 3 {
		t.Fatalf("saved draft = %#v err=%v", draft, err)
	}
}

func TestReviewSubmitPopupAndResult(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	path := store.PullRequestReviewDraft(m.root, 12)
	draft := gh.NewReviewDraft(12, "abc123")
	draft.Body = "Changes required"
	if err := gh.SaveReviewDraft(path, draft); err != nil {
		t.Fatal(err)
	}

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = u.(Model)
	if m.reviewSubmitEvent == "" || !strings.Contains(m.renderReviewSubmitPopup(), "Request changes") || !strings.Contains(m.renderReviewSubmitPopup(), "e edit comment") {
		t.Fatalf("submit popup not opened: event=%q popup=%q", m.reviewSubmitEvent, m.renderReviewSubmitPopup())
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = u.(Model)
	if m.localEditMode != editReviewBody || m.localEditor.Value() != "Changes required" {
		t.Fatalf("submit popup did not open review comment editor: mode=%v value=%q", m.localEditMode, m.localEditor.Value())
	}
	m.localEditor.SetValue("Please fix this before approve")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)
	if m.reviewDraft.Body != "Please fix this before approve" || m.reviewSubmitEvent == "" {
		t.Fatalf("edited review comment not kept: body=%q event=%q", m.reviewDraft.Body, m.reviewSubmitEvent)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = u.(Model)
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd == nil || !m.reviewSubmitting {
		t.Fatalf("changes request not scheduled: submitting=%v cmd=%v", m.reviewSubmitting, cmd)
	}
	u, _ = m.Update(reviewSubmitted{event: gh.ReviewRequestChangesEvent})
	m = u.(Model)
	if m.reviewSubmitting || !strings.Contains(m.notice, "REQUEST_CHANGES") {
		t.Fatalf("submit result = submitting:%v notice:%q", m.reviewSubmitting, m.notice)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("submitted draft remains: %v", err)
	}
}
