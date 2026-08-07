package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/embeddedterm"
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

func TestCachedCommentRendersMarkdownAndOpensBrowser(t *testing.T) {
	m := testModel()
	comment := gh.Comment{ID: 42, NodeID: "IC_42", Body: "**reviewed** ![image](https://example.com/image.png)", CreatedAt: "2026-08-01T10:00:00Z", HTMLURL: "https://github.com/acme/repo/pull/1#issuecomment-42"}
	comment.User.Login = "alice"
	m.cache.Comments = []gh.Comment{comment}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	out := m.View()
	for _, want := range []string{"@alice", "reviewed", "https://example.com/image.png"} {
		if !strings.Contains(out, want) {
			t.Fatalf("comment view missing %q", want)
		}
	}
	if strings.Contains(out, "**reviewed**") || strings.Contains(out, "![image]") {
		t.Fatalf("comment Markdown was not rendered: %q", out)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")}); cmd == nil {
		t.Fatal("o should open the selected GitHub comment")
	}
	cmd := browserCommand(comment.HTMLURL)
	if got := cmd.Args[len(cmd.Args)-1]; got != comment.HTMLURL {
		t.Fatalf("browser target = %q", got)
	}
}

func TestCommentSelectionSurvivesRefresh(t *testing.T) {
	m := testModel()
	comment := gh.Comment{ID: 42, NodeID: "IC_42", Body: "selected", CreatedAt: "2026-08-01T10:00:00Z"}
	m.cache.Comments = []gh.Comment{comment}
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	m.cursors[conversationTab] = 2

	newer := gh.Comment{ID: 43, NodeID: "IC_43", Body: "new", CreatedAt: "2026-08-02T10:00:00Z"}
	u, _ := m.Update(githubRefreshed{pr: gh.PR{Number: 1}, comments: []gh.Comment{comment, newer}})
	m = u.(Model)
	if selected := m.selectedComment(); selected == nil || selected.NodeID != "IC_42" {
		t.Fatalf("selection moved after refresh: %#v", selected)
	}
}

func TestGitHubCommentsAreBoxedAndActivityIsUnboxed(t *testing.T) {
	m := testModel()
	comment := gh.Comment{ID: 1, Body: "**comment**", CreatedAt: "2026-08-01T10:00:00Z"}
	comment.User.Login = "alice"
	activity := gh.Activity{ID: 2, Event: "labeled", CreatedAt: "2026-08-01T10:01:00Z"}
	activity.Actor.Login = "bob"
	activity.Label.Name = "bug"

	commentLines := m.commentLines(comment, false, 60)
	activityLines := m.activityLines(activity, false)
	commentView := strings.Join(commentLines, "\n")
	if !strings.Contains(commentView, "╭") || strings.Contains(commentView, "github ·") {
		t.Fatalf("GitHub comment should use a source-free card: %q", commentLines)
	}
	activityView := strings.Join(activityLines, "\n")
	if strings.Contains(activityView, "╭") || strings.Contains(activityView, "github ·") || !strings.Contains(activityView, "labeled") {
		t.Fatalf("GitHub activity should be an unboxed source-free row: %q", activityLines)
	}
	if cBorder == cCloudBorder {
		t.Fatal("local and GitHub cards must use different border intensity")
	}
}

func TestLocalCommentsMatchCloudCardsAndGitCommitsStayUnboxed(t *testing.T) {
	m := testModel()
	local := strings.Join(m.eventLines(m.events[0], false, 60), "\n")
	commit := strings.Join(m.eventLines(m.events[1], false, 60), "\n")
	if !strings.Contains(local, "╭") || strings.Contains(local, "local ·") || !strings.Contains(local, "claude-agent") {
		t.Fatalf("local event should be a source-free card: %q", local)
	}
	if strings.Contains(commit, "╭") || !strings.Contains(commit, "git") || !strings.Contains(commit, "committed") {
		t.Fatalf("git commit should be an unboxed sourced row: %q", commit)
	}
}

func TestConversationOrdersRFC3339OffsetsChronologically(t *testing.T) {
	m := testModel()
	earlier := gh.Comment{ID: 1, CreatedAt: "2026-08-01T10:00:00+09:00"}
	later := gh.Comment{ID: 2, CreatedAt: "2026-08-01T02:00:00Z"}
	m.events = nil
	m.cache.Comments = []gh.Comment{later, earlier}
	items := m.conversationItems()
	if len(items) != 2 || items[0].comment.ID != 1 || items[1].comment.ID != 2 {
		t.Fatalf("comments not ordered by instant: %#v", items)
	}
}

func TestCommentFailureKeepsCachedCommentsAndUpdatesPR(t *testing.T) {
	m := testModel()
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	m.cache.Comments = []gh.Comment{{ID: 1, Body: "cached"}}
	m.cache.Activities = []gh.Activity{{ID: 2, Event: "labeled"}}
	u, _ := m.Update(githubRefreshed{pr: gh.PR{Number: 9}, commentsErr: errors.New("comments unavailable"), activitiesErr: errors.New("activity unavailable")})
	m = u.(Model)
	if m.cache.PR == nil || m.cache.PR.Number != 9 || len(m.cache.Comments) != 1 || len(m.cache.Activities) != 1 {
		t.Fatalf("partial refresh lost usable state: %#v", m.cache)
	}
	if !strings.Contains(m.githubStatus, "comments/activity stale") {
		t.Fatalf("missing partial-refresh status: %q", m.githubStatus)
	}
}

func TestRefreshAndPublishAreMutuallyExclusive(t *testing.T) {
	m := testModel()
	m.refreshing = true
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = u.(Model)
	if cmd != nil || m.publishing || !strings.Contains(m.status, "wait") {
		t.Fatal("publish should wait for refresh")
	}
	m.refreshing, m.publishing = false, true
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); cmd != nil {
		t.Fatal("refresh should not overlap publish")
	}
}

func TestTranslateDiffMouseUsesContentBounds(t *testing.T) {
	msg := tea.MouseMsg{X: 42, Y: headerLines, Action: tea.MouseActionPress}
	local, ok := translateDiffMouse(msg, 40, 80, 20)
	if !ok || local.X != 0 || local.Y != 0 {
		t.Fatalf("translated = %+v, ok=%v", local, ok)
	}
	for _, outside := range []tea.MouseMsg{
		{X: 41, Y: headerLines},
		{X: 122, Y: headerLines},
		{X: 42, Y: headerLines - 1},
		{X: 42, Y: headerLines + 20},
	} {
		if _, ok := translateDiffMouse(outside, 40, 80, 20); ok {
			t.Fatalf("outside event accepted: %+v", outside)
		}
	}
}

func TestShiftTabRoutesKeysToEmbeddedCodeReview(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("Shift+Tab should focus CodeReview")
	}
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = u.(Model)
	if cmd != nil {
		t.Fatal("focused q should be forwarded, not quit live-pr")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if m.focusDiff {
		t.Fatal("second Shift+Tab should return focus to local PR")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("local PR q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit command returned %T", cmd())
	}
}

func TestConfiguredDiffDisplayIsAsyncCachedAndRejectsStaleResults(t *testing.T) {
	m := testModel()
	m.diffDisplay = "sed 's/foo/bar/g'"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	detail := detailContent{key: "commit:abc", raw: "foo", renderable: true}
	cmd := m.syncDetail(detail)
	if cmd == nil || !strings.Contains(m.detail.View(), "foo") {
		t.Fatal("raw diff should display while configured command runs")
	}
	if duplicate := m.syncDetail(detail); duplicate != nil {
		t.Fatal("same diff command should be single-flight")
	}
	msg := cmd().(diffRendered)
	u, _ = m.Update(msg)
	m = u.(Model)
	if !strings.Contains(m.detail.View(), "bar") {
		t.Fatalf("rendered diff not applied: %q", m.detail.View())
	}
	if cached := m.syncDetail(detail); cached != nil {
		t.Fatal("rendered diff should be cached")
	}

	m.detail.SetContent("current")
	m.detailKey = "current-key"
	u, _ = m.Update(diffRendered{key: "stale-key", output: "stale", raw: "raw", err: errors.New("diff display stale failure")})
	m = u.(Model)
	if strings.Contains(m.detail.View(), "stale") || strings.Contains(m.status, "stale failure") {
		t.Fatal("late result affected the current selection")
	}
}

func TestDefaultDiffDisplayUsesRawOutput(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	if cmd := m.syncDetail(detailContent{key: "commit:abc", raw: "raw diff", renderable: true}); cmd != nil || !strings.Contains(m.detail.View(), "raw diff") {
		t.Fatal("empty config must keep raw Git output without a command")
	}
}

func TestConfiguredDiffFailureKeepsRawOutput(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	m.diffDisplay = "exit 2"
	detail := detailContent{key: "commit:abc", raw: "raw diff", renderable: true}
	cmd := m.syncDetail(detail)
	u, _ = m.Update(cmd())
	m = u.(Model)
	if !strings.Contains(m.detail.View(), "raw diff") || !strings.Contains(m.status, "diff display") {
		t.Fatalf("failure did not fall back to raw diff: detail=%q status=%q", m.detail.View(), m.status)
	}
	if retry := m.syncDetail(detail); retry != nil || !strings.Contains(m.detail.View(), "raw diff") || m.status != "" {
		t.Fatalf("failed result should be cached and its error cleared after navigation")
	}
}

func TestPublishIsExplicitAndSingleFlight(t *testing.T) {
	m := testModel()
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = u.(Model)
	if cmd == nil || !m.publishing {
		t.Fatal("p should explicitly start publishing")
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")}); duplicate != nil {
		t.Fatal("a second publish must not start while one is in flight")
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
