package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/event"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/publish"
)

func TestConversationCacheInvalidation(t *testing.T) {
	m := testModel()
	first := m.conversationItems()
	second := m.conversationItems()
	if len(first) == 0 || &first[0] != &second[0] {
		t.Fatal("conversation derivation was not reused")
	}
	m.events = append(m.events, event.Event{TS: "2026-07-21T12:00", Kind: event.Note, Title: "new"})
	m.invalidateConversation()
	if got := m.conversationItems(); len(got) != len(first)+1 {
		t.Fatalf("invalidated conversation length = %d, want %d", len(got), len(first)+1)
	}
}

func TestConversationExcludesCommits(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	if items := m.conversationItems(); len(items) != 1 || items[0].event.Kind == event.Commit {
		t.Fatalf("conversation items = %#v", items)
	}
}

func TestConversationCountsStayAtPaneBottom(t *testing.T) {
	m := testModel()
	m.screen, m.active = detailScreen, conversationTab
	m.cache.Comments = []gh.Comment{{ID: 1}, {ID: 2}}
	m.cache.Activities = []gh.Activity{{ID: 3}}
	m.cache.PR = &gh.PR{Commits: []gh.PRCommit{
		{OID: "failed1", CommittedDate: "2026-07-21T10:30:00Z", MessageHeadline: "first", CheckRollupState: "FAILURE"},
		{OID: "passed2", CommittedDate: "2026-07-21T11:30:00Z", MessageHeadline: "second", CheckRollupState: "SUCCESS"},
	}}
	m.w, m.h = 120, 18
	m.layout()
	m.sync()
	m.list.GotoBottom()
	plain := ansi.Strip(m.View())
	counts := "1 events · 2 comments · 3 activity"
	if strings.Count(plain, counts) != 1 {
		t.Fatalf("Conversation counts are not fixed once: %q", plain)
	}
	lines := strings.Split(plain, "\n")
	leftBottom := lines[m.headerHeight()+m.detail.Height]
	if !strings.Contains(leftBottom, counts) {
		t.Fatalf("Conversation counts are not at pane bottom: %q", leftBottom)
	}
}

func TestConversationCompactsOnlyAdjacentActivityRows(t *testing.T) {
	m := testModel()
	m.events = nil
	m.commits = nil
	m.cache.Comments = []gh.Comment{{ID: 1, Body: "comment", CreatedAt: "2026-08-01T10:00:00Z"}}
	m.cache.Activities = []gh.Activity{
		{ID: 2, Event: "labeled", CreatedAt: "2026-08-01T11:00:00Z"},
		{ID: 3, Event: "closed", CreatedAt: "2026-08-01T12:00:00Z"},
	}
	m.cache.Activities[0].Actor.Login, m.cache.Activities[0].Label.Name = "alice", "demo"
	m.cache.Activities[1].Actor.Login = "bob"
	out, _ := m.buildConversation()
	lines := strings.Split(ansi.Strip(out), "\n")
	first, second := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "@alice") && strings.Contains(line, "labeled") {
			first = i
		}
		if strings.Contains(line, "@bob") && strings.Contains(line, "closed") {
			second = i
		}
	}
	if first < 1 || lines[first-1] != "" {
		t.Fatalf("comment/activity spacing changed: %q", ansi.Strip(out))
	}
	if second != first+1 {
		t.Fatalf("adjacent activities were not compacted: %q", ansi.Strip(out))
	}
}

func TestCachedPRDescriptionIsConversationOpeningCard(t *testing.T) {
	m := testModel()
	m.summary = "<final pull request summary>"
	m.events = []event.Event{{TS: "2026-07-21T11:00", Kind: event.Commit, Title: "feat: hidden"}}
	m.commits = nil
	m.cache.PR = &gh.PR{
		URL:       "https://github.com/acme/repo/pull/14",
		Body:      "**opening** ![image](https://example.com/image.png)",
		Author:    gh.PRUser{Login: "shonenm"},
		CreatedAt: "2026-08-07T14:49:25Z",
	}
	m.cache.Comments = []gh.Comment{{ID: 1, Body: "review comment", CreatedAt: "2026-08-07T15:00:00Z"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	items := m.conversationItems()
	if len(items) != 2 || items[0].pr == nil || items[0].event != nil || items[1].comment == nil {
		t.Fatalf("conversation items = %#v", items)
	}
	out := ansi.Strip(m.View())
	for _, want := range []string{"@shonenm", "description", "opening", "https://example.com/image.png", "review comment"} {
		if !strings.Contains(out, want) {
			t.Fatalf("description view missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "**opening**") || strings.Contains(out, "![image]") || strings.Contains(out, "feat: hidden") || strings.Contains(out, "final pull request summary") {
		t.Fatalf("description Markdown or hidden commit rendered incorrectly: %q", out)
	}
	if got := m.selectedBrowseURL(); got != m.cache.PR.URL {
		t.Fatalf("description browse URL = %q", got)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")}); cmd == nil {
		t.Fatal("o should open the selected PR description")
	}
}

func TestEmptyPRDescriptionHasPlaceholder(t *testing.T) {
	m := testModel()
	m.events = nil
	m.cache.PR = &gh.PR{URL: "https://example/pr/1"}
	lines := ansi.Strip(strings.Join(m.descriptionLines(*m.cache.PR, false, 60), "\n"))
	if !strings.Contains(lines, "(no description provided)") {
		t.Fatalf("empty description = %q", lines)
	}
}

func TestUserIconKeepsOneCell(t *testing.T) {
	m := testModel()
	m.avatarColors = map[string]string{"alice": "#ff0000"}
	if icon := m.userIcon("alice"); lipgloss.Width(icon) != 1 || ansi.Strip(icon) != "●" {
		t.Fatalf("user icon = %q", icon)
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
	m.cursors[conversationTab] = 1

	newer := gh.Comment{ID: 43, NodeID: "IC_43", Body: "new", CreatedAt: "2026-08-02T10:00:00Z"}
	u, _ := m.Update(githubRefreshed{pr: gh.PR{Number: 1}, comments: []gh.Comment{comment, newer}})
	m = u.(Model)
	if selected := m.selectedConversationItem(); selected == nil || selected.comment == nil || selected.comment.NodeID != "IC_42" {
		t.Fatalf("selection moved after refresh: %#v", selected)
	}
}

func TestDescriptionOrderAndSelectionSurviveRefresh(t *testing.T) {
	m := testModel()
	m.events = []event.Event{{TS: "2026-07-01T10:01:00Z", Kind: event.Note, Title: "local"}}
	m.cache.PR = &gh.PR{URL: "https://example/pr/1", Body: "old", CreatedAt: "2026-08-01T10:00:00Z"}
	m.cache.Comments = []gh.Comment{{ID: 2, CreatedAt: "2026-08-01T10:02:00Z"}}
	m.cache.Activities = []gh.Activity{{ID: 3, CreatedAt: "2026-08-01T10:03:00Z"}}
	m.cachePath = filepath.Join(t.TempDir(), "github.json")

	items := m.conversationItems()
	if len(items) != 4 || items[0].pr == nil || items[1].event == nil || items[2].comment == nil || items[3].activity == nil {
		t.Fatalf("conversation order = %#v", items)
	}
	m.cursors[conversationTab] = 0
	u, _ := m.Update(githubRefreshed{
		pr:         gh.PR{URL: "https://example/pr/1", Body: "edited", CreatedAt: "2026-08-01T10:00:00Z"},
		comments:   m.cache.Comments,
		activities: m.cache.Activities,
	})
	m = u.(Model)
	selected := m.selectedConversationItem()
	if selected == nil || selected.pr == nil || selected.pr.Body != "edited" || m.cursors[conversationTab] != 0 {
		t.Fatalf("description selection after refresh = %#v cursor=%d", selected, m.cursors[conversationTab])
	}
}

func TestLocalEventSelectionSurvivesCommitInsertion(t *testing.T) {
	m := testModel()
	m.events = []event.Event{
		{TS: "2026-08-01T10:01:00Z", Kind: event.Note, Title: "first"},
		{TS: "2026-08-01T10:02:00Z", Kind: event.Decision, Title: "selected", Body: "keep me"},
	}
	m.cursors[conversationTab] = 1
	selectedKey := m.selectedConversationKey()
	m.events = append([]event.Event{{TS: "2026-08-01T10:00:00Z", Kind: event.Commit, Title: "inserted", SHA: "abc"}}, m.events...)
	m.restoreConversationSelection(selectedKey)

	selected := m.selectedConversationItem()
	if selected == nil || selected.event == nil || selected.event.Title != "selected" || m.cursors[conversationTab] != 1 {
		t.Fatalf("selection after commit insertion = %#v cursor=%d", selected, m.cursors[conversationTab])
	}
}

func TestPublishInsertionPreservesConversationSelection(t *testing.T) {
	m := testModel()
	m.events = []event.Event{{TS: "2026-08-01T10:01:00Z", Kind: event.Note, Title: "selected"}}
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	cache := gh.NewCache(m.head)
	cache.PR = &gh.PR{URL: "https://example/pr/1", Body: "new", CreatedAt: "2026-08-01T10:00:00Z"}
	if err := gh.SaveCache(m.cachePath, cache); err != nil {
		t.Fatal(err)
	}

	u, _ := m.Update(publishDone{result: publish.Result{PR: *cache.PR, Created: true}})
	m = u.(Model)
	selected := m.selectedConversationItem()
	if selected == nil || selected.event == nil || selected.event.Title != "selected" || m.cursors[conversationTab] != 1 {
		t.Fatalf("selection after publish = %#v cursor=%d", selected, m.cursors[conversationTab])
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

func TestLocalEventsShowAuthorAndEditedState(t *testing.T) {
	m := testModel()
	agent := strings.Join(m.eventLines(m.events[0], false, 60), "\n")
	if !strings.Contains(agent, "╭") || strings.Contains(agent, "local ·") || !strings.Contains(agent, "agent") {
		t.Fatalf("agent event should be a source-free card: %q", agent)
	}
	user := strings.Join(m.eventLines(event.Event{Kind: event.Decision, Title: "chosen", Author: "user", UpdatedAt: "2026-01-01T10:00"}, false, 60), "\n")
	if !strings.Contains(user, "you") || !strings.Contains(user, "edited") {
		t.Fatalf("user event metadata = %q", user)
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

// A refresh that changes card content without changing cursor/width/item
// count must still re-render: restoreConversationSelection consumes the dirty
// flag before buildConversation runs, so the render cache has to be dropped
// by invalidateConversation itself.
// Background arrivals and reloads must not scroll the conversation back to
// the selected item: the reader may have scrolled away from it deliberately.
func TestConversationKeepsScrollAcrossSyncsAndReloads(t *testing.T) {
	m := testModel()
	m.screen, m.active = detailScreen, conversationTab
	m.ready, m.w, m.h = true, 120, 30
	pr := gh.PR{Number: 1, URL: "u", Body: "body"}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &pr
	for i := 1; i <= 40; i++ {
		c := gh.Comment{ID: int64(i), Body: "comment body", CreatedAt: "2026-08-01T10:00:00Z"}
		c.User.Login = "alice"
		m.cache.Comments = append(m.cache.Comments, c)
	}
	m.conversationDirty = true
	m.layout()
	m.sync()

	scrollQuarter(&m.list, true)
	scrollQuarter(&m.list, true)
	scrolled := m.list.YOffset
	if scrolled == 0 {
		t.Fatal("setup: the conversation did not scroll")
	}

	m.sync()
	if m.list.YOffset != scrolled {
		t.Fatalf("a background sync moved the view to %d, want %d", m.list.YOffset, scrolled)
	}

	u, _ := m.Update(githubRefreshed{generation: m.targetGeneration, pr: pr, comments: m.cache.Comments})
	if got := u.(Model).list.YOffset; got != scrolled {
		t.Fatalf("a reload moved the view to %d, want %d", got, scrolled)
	}

	// Moving the selection still pulls it into view.
	m.list.SetYOffset(scrolled)
	m.cursors[conversationTab] = len(m.conversationItems()) - 1
	m.sync()
	if m.list.YOffset == scrolled {
		t.Fatal("selection change no longer scrolls the conversation")
	}
}

func TestConversationRefreshInvalidatesRenderCache(t *testing.T) {
	m := testModel()
	m.list.Width = 80
	c := gh.Comment{ID: 1, Body: "old body", CreatedAt: "2026-08-01T10:00:00Z"}
	c.User.Login = "alice"
	m.cache.Comments = []gh.Comment{c}
	m.conversationDirty = true
	before, _ := m.buildConversation()
	if !strings.Contains(ansi.Strip(before), "old body") {
		t.Fatal("setup: old body missing")
	}

	// Refresh path: content changes, then selection restore consumes dirty.
	m.cache.Comments[0].Body = "new body"
	m.invalidateConversation()
	m.restoreConversationSelection(m.selectedConversationKey())
	after, _ := m.buildConversation()
	if !strings.Contains(ansi.Strip(after), "new body") {
		t.Fatal("render cache served stale conversation content")
	}
}

func TestConversationCursorMoveReusesUnselectedCardRenders(t *testing.T) {
	m := testModel()
	m.list.Width = 80
	one := gh.Comment{ID: 1, Body: "first"}
	two := gh.Comment{ID: 2, Body: "second"}
	m.cache.Comments = []gh.Comment{one, two}
	m.conversationDirty = true
	items := m.conversationItems()
	if len(items) < 2 {
		t.Fatalf("need 2+ items, got %d", len(items))
	}
	m.cursors[conversationTab] = 0
	_, _ = m.buildConversation()
	cached := len(m.convItemCache)
	if cached == 0 {
		t.Fatal("no unselected cards cached")
	}
	// Cursor move: previously-selected card renders once, the rest come from
	// the cache.
	m.cursors[conversationTab] = 1
	_, _ = m.buildConversation()
	if len(m.convItemCache) != cached+1 {
		t.Fatalf("cursor move cache growth = %d, want %d", len(m.convItemCache), cached+1)
	}
	// Content changes flush the card cache.
	m.invalidateConversation()
	if len(m.convItemCache) != 0 {
		t.Fatal("invalidation kept stale card renders")
	}
}

func TestSpecialCardsGetTheirOwnBorder(t *testing.T) {
	// Verdicts carry GitHub's semantics; anything else keeps the plain frame.
	for state, want := range map[string]string{
		"APPROVED":          cGreenF,
		"approved":          cGreenF,
		"CHANGES_REQUESTED": cRedF,
		"COMMENTED":         cCloudBorder,
		"DISMISSED":         cCloudBorder,
		"":                  cCloudBorder,
	} {
		if got := reviewBorder(state); got != want {
			t.Fatalf("%q border = %q, want %q", state, got, want)
		}
	}
	// Each emphasized frame has to be distinguishable from the others, from
	// ordinary comments, and from the selection frame.
	frames := map[string]string{"approve": cGreenF, "change request": cRedF, "description": cDescriptionBorder}
	for name, color := range frames {
		if color == cCloudBorder || color == cAccent {
			t.Fatalf("%s frame is not distinguishable: %q", name, color)
		}
		for other, otherColor := range frames {
			if other != name && otherColor == color {
				t.Fatalf("%s and %s share the frame color %q", name, other, color)
			}
		}
	}

	// Rendering in tests strips color, so just confirm the cards still frame
	// their content.
	m := testModel()
	m.list.Width = 80
	for name, lines := range map[string][]string{
		"approval":    m.reviewLines(gh.Review{State: "APPROVED", Body: "lgtm"}, false, 80),
		"description": m.descriptionLines(gh.PR{Number: 1, Body: "why"}, false, 80),
	} {
		got := ansi.Strip(strings.Join(lines, "\n"))
		if !strings.Contains(got, "╭") || !strings.Contains(got, "╰") {
			t.Fatalf("%s card lost its frame: %q", name, got)
		}
	}
}
