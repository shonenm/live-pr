package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/publish"
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

func TestHeaderShowsCachedPRAssigneesAndLabels(t *testing.T) {
	m := testModel()
	m.w = 120
	m.cache.PR = &gh.PR{
		Number:    12,
		State:     "OPEN",
		Assignees: []gh.PRUser{{Login: "alice"}, {Login: "bob"}},
		Labels:    []gh.PRLabel{{Name: "bug", Color: "d73a4a"}, {Name: "docs", Color: "fef2c0"}},
	}
	plain := ansi.Strip(m.renderHeader())
	for _, want := range []string{"#12 open", "@alice @bob", "bug", "docs"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("header missing %q: %q", want, plain)
		}
	}
	if m.headerHeight() != headerBaseLines+1 {
		t.Fatalf("PR header height = %d", m.headerHeight())
	}
	m.w = 25
	if width := lipgloss.Width(m.renderPRMeta(*m.cache.PR)); width > m.w {
		t.Fatalf("metadata width = %d, want <= %d", width, m.w)
	}
	m.cache.PR = nil
	if m.headerHeight() != headerBaseLines {
		t.Fatalf("local header height = %d", m.headerHeight())
	}
}

func TestLabelForegroundChoosesHigherContrast(t *testing.T) {
	if got := contrastingLabelForeground(0x00aa00); got != "#0d1117" {
		t.Fatalf("green foreground = %s", got)
	}
	if got := contrastingLabelForeground(0x000080); got != "#ffffff" {
		t.Fatalf("navy foreground = %s", got)
	}
}

func TestMainStartupShowsListWithoutCreatingBranchStore(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "file")
	run("commit", "-m", "main")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if m.screen != prListScreen {
		t.Fatalf("main startup screen = %v", m.screen)
	}
	if _, err := os.Stat(filepath.Join(dir, ".live-pr", "main")); !os.IsNotExist(err) {
		t.Fatalf("main branch store was created: err=%v", err)
	}
}

func TestCurrentPRIdentityRejectsSameNamedFork(t *testing.T) {
	if !isCurrentPR(gh.PR{HeadRefName: "feature"}, "feature") {
		t.Fatal("same-repository head should be current")
	}
	if isCurrentPR(gh.PR{HeadRefName: "feature", IsCrossRepository: true}, "feature") {
		t.Fatal("same-named fork must be remote")
	}
}

func TestStartupRouting(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		branch, defaultBranch              string
		hasPR, hasData, hasChanges, detail bool
	}{
		{name: "open PR", branch: "feature", defaultBranch: "main", hasPR: true, detail: true},
		{name: "local timeline", branch: "feature", defaultBranch: "main", hasData: true, detail: true},
		{name: "local commits", branch: "feature", defaultBranch: "main", hasChanges: true, detail: true},
		{name: "main", branch: "main", defaultBranch: "main", hasPR: true},
		{name: "empty feature", branch: "feature", defaultBranch: "main"},
		{name: "detached", branch: "HEAD", defaultBranch: "main", hasChanges: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldOpenLocal(tc.branch, tc.defaultBranch, tc.hasPR, tc.hasData, tc.hasChanges); got != tc.detail {
				t.Fatalf("shouldOpenLocal = %v, want %v", got, tc.detail)
			}
		})
	}
}

func TestPRListNavigationAndRefresh(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.openPRs = []gh.PR{{Number: 1, Title: "first"}, {Number: 2, Title: "second"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	if out := ansi.Strip(m.View()); !strings.Contains(out, "Open pull requests") || !strings.Contains(out, "#1") {
		t.Fatalf("PR list missing content: %q", out)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	if m.prCursor != 1 {
		t.Fatalf("PR cursor = %d", m.prCursor)
	}
	m.listRefreshing = false
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = u.(Model)
	if cmd == nil || !m.listRefreshing {
		t.Fatal("r should explicitly refresh the PR list")
	}
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q should quit the PR list")
	}
}

func TestPRListRefreshPreservesCacheAndSelection(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.defaultBranch = "main"
	m.navigatorPath = filepath.Join(t.TempDir(), "github-prs.json")
	m.navigator = gh.NewNavigatorCache()
	m.openPRs = []gh.PR{{Number: 1}, {Number: 2}}
	m.prCursor = 1
	u, _ := m.Update(prListRefreshed{err: errors.New("offline")})
	m = u.(Model)
	if len(m.openPRs) != 2 || m.prCursor != 1 {
		t.Fatalf("failed refresh lost cache/selection: prs=%v cursor=%d", m.openPRs, m.prCursor)
	}
	u, _ = m.Update(prListRefreshed{prs: []gh.PR{{Number: 2}, {Number: 3}}})
	m = u.(Model)
	if len(m.openPRs) != 2 || m.openPRs[m.prCursor].Number != 2 {
		t.Fatalf("successful refresh lost selection: prs=%v cursor=%d", m.openPRs, m.prCursor)
	}
	cached, err := gh.LoadNavigatorCache(m.navigatorPath)
	if err != nil || len(cached.PRs) != 2 {
		t.Fatalf("navigator cache not saved: %#v err=%v", cached, err)
	}
}

func TestPRListEnterOpensRemoteWithoutChangingCheckout(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.openPRs = []gh.PR{{Number: 14, Title: "remote", HeadRefName: "feature", BaseRefName: "main", HeadRefOID: "abc"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd == nil || m.screen != detailScreen || !m.remote || m.head != "feature" || m.headRev != "refs/live-pr/pulls/14/head" || m.diffTerminal != nil {
		t.Fatalf("remote target not opened: screen=%v remote=%v head=%q rev=%q terminal=%v", m.screen, m.remote, m.head, m.headRev, m.diffTerminal)
	}
}

func TestStaleLocalAndPublishResultsCannotReplaceTarget(t *testing.T) {
	m := testModel()
	m.targetGeneration = 2
	m.currentBranch = "feature"
	m.cache.PR = &gh.PR{Number: 2, HeadRefName: "feature"}
	u, _ := m.Update(githubRefreshed{generation: 1, pr: gh.PR{Number: 1, HeadRefName: "feature"}})
	m = u.(Model)
	if m.cache.PR == nil || m.cache.PR.Number != 2 {
		t.Fatalf("stale local refresh replaced target: %#v", m.cache.PR)
	}
	m.publishing = true
	u, _ = m.Update(publishDone{generation: 1, result: publish.Result{PR: gh.PR{Number: 1}}})
	m = u.(Model)
	if m.cache.PR == nil || m.cache.PR.Number != 2 {
		t.Fatalf("stale publish replaced target: %#v", m.cache.PR)
	}
}

func TestStaleRemoteResultCannotReplaceNewTarget(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.openPRs = []gh.PR{
		{Number: 1, Title: "A", HeadRefName: "a", BaseRefName: "main"},
		{Number: 2, Title: "B", HeadRefName: "b", BaseRefName: "main"},
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	generationA := m.targetGeneration
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = u.(Model)
	m.prCursor = 1
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if m.cache.PR == nil || m.cache.PR.Number != 2 {
		t.Fatalf("target B not opened: %#v", m.cache.PR)
	}
	u, _ = m.Update(remoteLoaded{generation: generationA, pr: gh.PR{Number: 1}, headRef: "HEAD"})
	m = u.(Model)
	if m.cache.PR == nil || m.cache.PR.Number != 2 || m.diffTerminal != nil {
		t.Fatalf("stale A replaced B: %#v terminal=%v", m.cache.PR, m.diffTerminal)
	}
}

func TestPRListScrollTracksRenderedRows(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	for i := 1; i <= 20; i++ {
		m.openPRs = append(m.openPRs, gh.PR{Number: i, Title: "PR"})
	}
	m.prCursor = 10
	u, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = u.(Model)
	if m.list.YOffset < 20 {
		t.Fatalf("list offset did not follow rendered rows: %d", m.list.YOffset)
	}
}

func TestRemoteLoadedStartsReviewAndCachesConversation(t *testing.T) {
	m := testModel()
	m.root = t.TempDir()
	m.currentBranch = "main"
	m.remote = true
	m.screen = detailScreen
	m.diffCommand = "cat"
	m.navigator = gh.NewNavigatorCache()
	m.navigatorPath = filepath.Join(t.TempDir(), "github-prs.json")
	pr := gh.PR{Number: 14, URL: "https://example/pr/14", Title: "remote", HeadRefName: "feature", BaseRefName: "main"}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &pr
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, cmd := m.Update(remoteLoaded{pr: pr, headRef: "HEAD", comments: []gh.Comment{{ID: 1, Body: "cached"}}})
	m = u.(Model)
	defer m.close()
	if cmd == nil || m.diffTerminal == nil || m.refreshing || len(m.cache.Comments) != 1 {
		t.Fatalf("remote load incomplete: terminal=%v refreshing=%v cache=%#v", m.diffTerminal, m.refreshing, m.cache)
	}
	cached, err := gh.LoadNavigatorCache(m.navigatorPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, ok := cached.Snapshot(14); !ok || len(snapshot.Comments) != 1 {
		t.Fatalf("remote snapshot = %#v ok=%v", snapshot, ok)
	}
}

func TestDetailBReturnsToPRList(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	m.focusDiff = true
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = u.(Model)
	if m.screen != prListScreen || m.diffTerminal != nil {
		t.Fatalf("b did not return to list: screen=%v terminal=%v", m.screen, m.diffTerminal)
	}
}

func TestViewRendersHeaderAndTimeline(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	out := m.View()
	for _, want := range []string{"Local", "main", "feature/x", "1 files changed", "decision", "chose Go"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestConversationExcludesCommits(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	if items := m.conversationItems(); len(items) != 1 || items[0].event.Kind == event.Commit {
		t.Fatalf("conversation items = %#v", items)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	if m.cursors[conversationTab] != 0 {
		t.Fatalf("cursor moved into hidden commits: %d", m.cursors[conversationTab])
	}
}

func TestCachedPRDescriptionIsConversationOpeningCard(t *testing.T) {
	m := testModel()
	m.events = []event.Event{{TS: "2026-07-21T11:00", Kind: event.Commit, Title: "feat: hidden"}}
	m.cache.PR = &gh.PR{
		URL:       "https://github.com/acme/repo/pull/14",
		Body:      "**opening** ![image](https://example.com/image.png)",
		Author:    gh.PRUser{Login: "shonenm"},
		CreatedAt: "2026-08-07T14:49:25Z",
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	items := m.conversationItems()
	if len(items) != 1 || items[0].pr == nil || items[0].event != nil {
		t.Fatalf("conversation items = %#v", items)
	}
	out := ansi.Strip(m.View())
	for _, want := range []string{"@shonenm", "description", "opening", "https://example.com/image.png"} {
		if !strings.Contains(out, want) {
			t.Fatalf("description view missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "**opening**") || strings.Contains(out, "![image]") || strings.Contains(out, "feat: hidden") {
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

func TestCommitPickerSelectsCommitAndEscRestoresBranchReview(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	if m.active != commitsTab || !strings.Contains(m.View(), "feat: x") {
		t.Fatalf("c should replace Conversation with the commit picker")
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if m.reviewSHA != "abc1234" || !strings.Contains(ansi.Strip(m.renderHeader()), "commit abc1234") {
		t.Fatalf("Enter did not select commit review: sha=%q", m.reviewSHA)
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if m.active != conversationTab || m.reviewSHA != "" || m.cursors[conversationTab] != 0 {
		t.Fatalf("Esc should restore branch review and the Conversation cursor")
	}
}

func TestCommitPickerCancelKeepsBranchTerminal(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	branchTerminal := m.diffTerminal
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	defer m.close()
	if m.active != conversationTab || m.diffTerminal != branchTerminal || m.reviewSHA != "" {
		t.Fatal("canceling an unselected picker restarted branch review")
	}
}

func TestPRRefreshAddsMetadataHeaderRow(t *testing.T) {
	m := testModel()
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	before := m.detail.Height
	u, _ = m.Update(githubRefreshed{pr: gh.PR{Number: 12, State: "OPEN"}})
	m = u.(Model)
	if m.detail.Height != before-1 || m.headerHeight() != headerBaseLines+1 {
		t.Fatalf("metadata row not reflected in layout: before=%d after=%d header=%d", before, m.detail.Height, m.headerHeight())
	}
}

func TestLocalRefreshRejectsSameNamedFork(t *testing.T) {
	m := testModel()
	m.currentBranch = "feature"
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	u, _ := m.Update(githubRefreshed{pr: gh.PR{Number: 9, HeadRefName: "feature", IsCrossRepository: true}})
	m = u.(Model)
	if m.cache.PR != nil || !strings.Contains(m.githubStatus, "Local only") {
		t.Fatalf("same-named fork bound as local PR: cache=%#v status=%q", m.cache.PR, m.githubStatus)
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
	m.events = []event.Event{{TS: "2026-08-01T10:01:00Z", Kind: event.Note, Title: "local"}}
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

func TestLocalEventsUseSourceFreeCards(t *testing.T) {
	m := testModel()
	local := strings.Join(m.eventLines(m.events[0], false, 60), "\n")
	if !strings.Contains(local, "╭") || strings.Contains(local, "local ·") || !strings.Contains(local, "claude-agent") {
		t.Fatalf("local event should be a source-free card: %q", local)
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
	headerHeight := headerBaseLines + 1
	msg := tea.MouseMsg{X: 42, Y: headerHeight, Action: tea.MouseActionPress}
	local, ok := translateDiffMouse(msg, 40, 80, 20, headerHeight)
	if !ok || local.X != 0 || local.Y != 0 {
		t.Fatalf("translated = %+v, ok=%v", local, ok)
	}
	for _, outside := range []tea.MouseMsg{
		{X: 41, Y: headerHeight},
		{X: 122, Y: headerHeight},
		{X: 42, Y: headerHeight - 1},
		{X: 42, Y: headerHeight + 20},
	} {
		if _, ok := translateDiffMouse(outside, 40, 80, 20, headerHeight); ok {
			t.Fatalf("outside event accepted: %+v", outside)
		}
	}
}

func TestReviewFocusKeys(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("l should focus review")
	}
	footer := ansi.Strip(m.renderFooter())
	if !strings.Contains(footer, "q / Shift+Tab: left pane") || strings.Contains(footer, "branch review") {
		t.Fatalf("focused footer is misleading: %q", footer)
	}
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = u.(Model)
	if cmd != nil || m.focusDiff {
		t.Fatal("q should return focus to the left pane")
	}
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Fatal("q on the left pane should quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("left-pane q returned %T", cmd())
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("Shift+Tab should focus review")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if m.focusDiff {
		t.Fatal("second Shift+Tab should return focus to the left pane")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C should quit from the left pane")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit command returned %T", cmd())
	}
}

func TestStaticReviewFocusKeys(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("l should focus the static review fallback")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = u.(Model)
	if m.focusDiff {
		t.Fatal("q should return from the static review fallback")
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

func TestCommitSelectionStartsEmbeddedCommitCommandAndFocusesReview(t *testing.T) {
	m := testModel()
	m.root = t.TempDir()
	m.diffCommitCommand = "cat"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	defer m.close()
	if cmd == nil || m.reviewSHA != "abc1234" || !m.focusDiff || m.diffTerminal == nil {
		t.Fatalf("commit selection did not start/focus embedded review: sha=%q focus=%v terminal=%v", m.reviewSHA, m.focusDiff, m.diffTerminal != nil)
	}
}
