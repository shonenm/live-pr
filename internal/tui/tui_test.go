package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

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
		title:       "CodeDiff review mode",
		prView:      allPRsView,
		diffCommand: "nvim",
		base:        "main",
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

func TestLoadDetailCachesRawGitOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\ncount=0\n[ -f %q ] && read count < %q\ncount=$((count + 1))\nprintf '%%s' \"$count\" > %q\nprintf 'cached diff\\n'\n", counter, counter, counter)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	m := testModel()
	m.screen, m.base, m.headRev = detailScreen, "main", "HEAD"
	m.diffCommand, m.diffTerminal = "", nil

	first, second := m.loadDetail(), m.loadDetail()
	calls, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if first.raw != "cached diff" || second.raw != first.raw || string(calls) != "1" {
		t.Fatalf("details = %#v / %#v, calls=%q", first, second, calls)
	}
	m.resetDetailCaches()
	_ = m.loadDetail()
	calls, _ = os.ReadFile(counter)
	if string(calls) != "2" {
		t.Fatalf("cache reset calls=%q", calls)
	}
}

func TestStaticDiffUsesFileExplorerAndChecksFiles(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.diffCommand = ""
	m.diffTerminal = nil
	m.base, m.headRev = "main", "HEAD"
	m.files = []git.ChangedFile{
		{Status: "M", Path: "internal/tui/tui.go"},
		{Status: "A", Path: "internal/tui/explorer.go"},
	}
	m.explorer.Width = 80

	content, selected := m.buildFileExplorer()
	plain := ansi.Strip(content)
	for _, want := range []string{"Files · 2 changed", "□ M internal/tui/tui.go", "□ A internal/tui/explorer.go"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("explorer missing %q: %q", want, plain)
		}
	}
	if selected != 1 {
		t.Fatalf("selected explorer row = %d, want 1", selected)
	}

	m.toggleFileCheck()
	content, _ = m.buildFileExplorer()
	plain = ansi.Strip(content)
	if !strings.Contains(plain, "✓ M internal/tui/tui.go") {
		t.Fatalf("checked file missing: %q", plain)
	}
}

func TestStaticDiffExplorerAndDiffNavigation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.diffCommand = ""
	m.ready = true
	m.files = []git.ChangedFile{
		{Status: "M", Path: "internal/tui/tui.go"},
		{Status: "A", Path: "internal/tui/explorer.go"},
	}
	m.focusExplorer = true
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	if m.fileCursor != 1 {
		t.Fatalf("file cursor = %d, want 1", m.fileCursor)
	}

	m.fileCursor = 0
	m.detail.Width, m.detail.Height = 40, 3
	m.detail.SetContent(strings.Repeat("line\n", 20))
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = u.(Model)
	if m.detail.YOffset == 0 {
		t.Fatal("ctrl+d did not scroll the diff")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = u.(Model)
	if m.fileCursor != 1 {
		t.Fatalf("G file cursor = %d, want 1", m.fileCursor)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = u.(Model)
	if m.fileCursor != 0 {
		t.Fatalf("gg file cursor = %d, want 0", m.fileCursor)
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	if !m.checkedFiles[m.fileKey(m.files[m.fileCursor])] {
		t.Fatal("c did not check the selected file from Diff")
	}
}

func TestReservedReviewKeysStayWithLivePR(t *testing.T) {
	if !reservedReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}) {
		t.Fatal("q should stay with live-pr")
	}
	if reservedReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) {
		t.Fatal("j should be forwarded to the reviewer")
	}
}

func TestStaticDiffFocusAndQReturnToConversation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.diffCommand = ""
	m.ready = true

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = u.(Model)
	if m.focusDiff || !m.focusExplorer {
		t.Fatalf("l focus = diff:%v explorer:%v", m.focusDiff, m.focusExplorer)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = u.(Model)
	if m.focusDiff || !m.focusExplorer {
		t.Fatalf("second l focus = diff:%v explorer:%v", m.focusDiff, m.focusExplorer)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = u.(Model)
	if m.focusDiff || m.focusExplorer {
		t.Fatal("q did not return focus to Conversation")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q from Conversation should quit")
	}
}

func TestCurrentPRSuppressesLocalPRRow(t *testing.T) {
	m := testModel()
	m.currentBranch = "feature/x"
	m.localAvailable = true
	pr := gh.PR{Number: 7, HeadRefName: "feature/x", BaseRefName: "main"}
	items := m.withLocalPR([]gh.PR{pr})
	if len(items) != 1 || items[0].Number != 7 {
		t.Fatalf("local row was not suppressed: %#v", items)
	}
	m.cache.PR = &pr
	items = m.withLocalPR(nil)
	if len(items) != 1 || items[0].Number != 7 {
		t.Fatalf("cached current PR was not listed: %#v", items)
	}
}

func TestCurrentBranchPRPrefersOpenThenClosed(t *testing.T) {
	prs := []gh.PR{
		{Number: 1, State: "MERGED", HeadRefName: "feature/x"},
		{Number: 2, State: "OPEN", HeadRefName: "feature/x"},
	}
	if got := currentBranchPR(prs, "feature/x"); got == nil || got.Number != 2 {
		t.Fatalf("current branch PR = %#v", got)
	}
	if got := currentBranchPR(prs[:1], "feature/x"); got == nil || got.Number != 1 {
		t.Fatalf("merged branch PR = %#v", got)
	}
}

func TestCurrentBranchResolutionKeepsLocalOrShowsMergedPR(t *testing.T) {
	m := testModel()
	m.screen, m.prView = prListScreen, assignedView
	m.currentBranch, m.defaultBranch = "feature/x", "main"
	m.localAvailable, m.autoOpenCurrent = true, true
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")

	u, _ := m.Update(currentBranchPRLoaded{err: gh.ErrPRNotFound})
	m = u.(Model)
	if len(m.openPRs) != 1 || m.openPRs[0].Number != 0 || m.openPRs[0].HeadRefName != "feature/x" {
		t.Fatalf("empty branch local PR = %#v", m.openPRs)
	}

	m.autoOpenCurrent = false
	u, _ = m.Update(currentBranchPRLoaded{pr: gh.PR{Number: 9, State: "MERGED", HeadRefName: "feature/x"}})
	m = u.(Model)
	if m.localAvailable || m.prView != closedPRsView || m.prListState != closedPRListState || len(m.openPRs) != 1 || m.openPRs[0].Number != 9 {
		t.Fatalf("merged branch PR = local:%v view:%v state:%v prs:%#v", m.localAvailable, m.prView, m.prListState, m.openPRs)
	}
}

func TestLocalPRAppearsInEveryOpenView(t *testing.T) {
	m := testModel()
	local := gh.PR{}
	for _, view := range []prView{assignedView, reviewRequestedView, allPRsView, authoredView, needsMeView} {
		if !m.matchesView(local, view) {
			t.Fatalf("local PR missing from %s", view)
		}
	}
	if m.matchesView(local, closedPRsView) || !matchesListState(gh.PR{State: "MERGED", Number: 1}, closedPRListState) {
		t.Fatal("local/merged state routing is incorrect")
	}
}

func TestPRListRefreshAssociatesCurrentLocalBranch(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.currentBranch = "feature/x"
	m.localAvailable = true
	m.prListGeneration = 1
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
	pr := gh.PR{Number: 8, HeadRefName: "feature/x", BaseRefName: "main", State: "OPEN", HeadRefOID: "head"}
	u, _ := m.Update(prListRefreshed{generation: 1, viewer: "me", prs: []gh.PR{pr}})
	m = u.(Model)
	if m.cache.PR == nil || m.cache.PR.Number != 8 || m.localAvailable {
		t.Fatalf("current PR association = cache:%#v local:%v", m.cache.PR, m.localAvailable)
	}
}

func TestDetailMergeStartsConfirmation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.cache.PR = &gh.PR{Number: 9, State: "OPEN", HeadRefOID: "head", HeadRefName: "feature/x"}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = u.(Model)
	if cmd != nil || m.pendingPRAction != mergePR || m.prActionNumber != 9 {
		t.Fatalf("detail merge confirmation = pending:%v number:%d cmd:%v", m.pendingPRAction, m.prActionNumber, cmd)
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

func TestPaletteMatchesPrimerDarkSemantics(t *testing.T) {
	got := []string{cFg, cMuted, cBorder, cCloudBorder, cAccent, cGreenF, cAttention, cRedF}
	want := []string{"#f0f6fc", "#9198a1", "#3d444d", "#656c76", "#4493f8", "#3fb950", "#d29922", "#f85149"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("palette[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestEventKindsUseDistinctSemanticColors(t *testing.T) {
	want := map[event.Kind]string{
		event.Decision: stAccent.Bold(true).Render(string(event.Decision)),
		event.Pivot:    stAttention.Bold(true).Render(string(event.Pivot)),
		event.Note:     stGreenF.Bold(true).Render(string(event.Note)),
		event.Commit:   stMuted.Bold(true).Render(string(event.Commit)),
	}
	for kind, expected := range want {
		if got := kindLabel(kind); got != expected {
			t.Fatalf("kind %s = %q, want %q", kind, got, expected)
		}
	}
	seen := map[string]event.Kind{}
	for _, kind := range []event.Kind{event.Note, event.Decision, event.Pivot, event.Summary, event.Commit} {
		label := kindLabel(kind)
		if other, dup := seen[label]; dup {
			t.Fatalf("kinds %s and %s render identically: %q", kind, other, label)
		}
		seen[label] = kind
	}
}

func TestGitHubSemanticStatesUseMatchingStyles(t *testing.T) {
	if got := reviewSummary("APPROVED"); got != stGreenF.Render("review approved") {
		t.Fatalf("approved review = %q", got)
	}
	if got := reviewSummary("CHANGES_REQUESTED"); got != stRedF.Render("review changes requested") {
		t.Fatalf("changes-requested review = %q", got)
	}
	if got := mergeSummary(gh.PR{Number: 1, MergeStateStatus: "BLOCKED"}); got != stRedF.Render("blocked") {
		t.Fatalf("blocked merge = %q", got)
	}
	if got := mergeSummary(gh.PR{Number: 1, Mergeable: "MERGEABLE", MergeStateStatus: "UNSTABLE"}); got != stGreenF.Render("⇄ mergeable") {
		t.Fatalf("unstable merge = %q", got)
	}
	if got := checkSummary([]gh.PRCheck{{Status: "IN_PROGRESS"}}); got != stAttention.Render("◐ CI 1 pending") {
		t.Fatalf("pending CI = %q", got)
	}
	if cDoneEmphasis != "#8957e5" {
		t.Fatalf("merged palette = %s", cDoneEmphasis)
	}
	if got := prStateBadgeColor("MERGED"); got != cDoneEmphasis {
		t.Fatalf("merged badge = %s", got)
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
	if out := ansi.Strip(m.View()); !strings.Contains(out, "Pull requests") || !strings.Contains(out, "#1") {
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

func TestPRListPreviewShowsConversationAndHealth(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.openPRs = []gh.PR{{
		Number: 15, Title: "navigator", Body: "## Summary\n\nPreview body", BaseRefName: "main", HeadRefName: "feature",
		Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", ReviewDecision: "APPROVED",
		ChangedFiles: 18, Additions: 1123, Deletions: 128, CommitCount: 5,
		Conversation: []gh.PRConversationComment{{Author: gh.PRUser{Login: "alice"}, Body: "Looks good", CreatedAt: "2026-08-08T00:00:00Z"}}, CommentCount: 1,
		Checks: []gh.PRCheck{{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}},
		Author: gh.PRUser{Login: "bob"}, Assignees: []gh.PRUser{{Login: "carol"}}, Labels: []gh.PRLabel{{Name: "feature", Color: "238636"}},
	}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	m = u.(Model)
	out := ansi.Strip(m.View())
	for _, want := range []string{"description", "comment", "Summary", "Preview body", "@alice", "Looks good", "mergeable", "CI 1 passed", "18 files", "+1123", "-128", "5 commits", "1 comments", "author @bob", "assigned @carol", "feature", "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "Conversation top") {
		t.Fatalf("preview should use cards instead of a Conversation top heading: %q", out)
	}
	if m.list.Width >= m.w || m.detail.Width <= m.list.Width {
		t.Fatalf("list preview layout = %d/%d total=%d", m.list.Width, m.detail.Width, m.w)
	}
}

func TestPRListVimNavigationAndNarrowLayout(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.openPRs = make([]gh.PR, 20)
	for i := range m.openPRs {
		m.openPRs[i] = gh.PR{Number: i + 1, Title: fmt.Sprintf("PR %d", i+1)}
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = u.(Model)
	if m.list.Width+m.detail.Width+3 > m.w {
		t.Fatalf("narrow layout overflow: list=%d detail=%d width=%d", m.list.Width, m.detail.Width, m.w)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	if m.prCursor != 0 {
		t.Fatalf("gg did not move PR list to top: %d", m.prCursor)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = u.(Model)
	if m.prCursor != len(m.openPRs)-1 {
		t.Fatalf("G did not move PR list to bottom: %d", m.prCursor)
	}
	m.detail.Height = 3
	m.detail.SetContent(strings.Repeat("preview\n", 20))
	m.detail.GotoBottom()
	bottomOffset := m.detail.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = u.(Model)
	if m.detail.YOffset >= bottomOffset {
		t.Fatal("Ctrl+U did not scroll PR preview up")
	}
	topOffset := m.detail.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = u.(Model)
	if m.detail.YOffset <= topOffset {
		t.Fatal("Ctrl+D did not scroll PR preview down")
	}
}

func TestConversationVimNavigation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.active = conversationTab
	for i := 0; i < 8; i++ {
		m.cache.Comments = append(m.cache.Comments, gh.Comment{NodeID: fmt.Sprintf("comment-%d", i), Body: fmt.Sprintf("comment %d", i), CreatedAt: "2026-08-08T00:00:00Z"})
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	if m.cursors[conversationTab] != 0 {
		t.Fatalf("conversation gg = %d", m.cursors[conversationTab])
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = u.(Model)
	if m.cursors[conversationTab] != m.activeLen()-1 {
		t.Fatalf("conversation G = %d, want %d", m.cursors[conversationTab], m.activeLen()-1)
	}
	m.list.Height = 3
	m.list.SetContent(strings.Repeat("conversation\n", 20))
	m.list.GotoBottom()
	bottomOffset := m.list.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = u.(Model)
	if m.list.YOffset >= bottomOffset {
		t.Fatal("conversation Ctrl+U did not scroll up")
	}
	topOffset := m.list.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = u.(Model)
	if m.list.YOffset <= topOffset {
		t.Fatal("conversation Ctrl+D did not scroll down")
	}
}

func TestPRListPreviewShowsConflictAndFailedCI(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.openPRs = []gh.PR{{Number: 1, Mergeable: "CONFLICTING", MergeStateStatus: "DIRTY", Checks: []gh.PRCheck{{Conclusion: "FAILURE"}}}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "conflicts") || !strings.Contains(out, "CI 1 failed") {
		t.Fatalf("health preview = %q", out)
	}
}

func TestCheckSummaryTreatsTerminalFailuresAsFailed(t *testing.T) {
	for _, conclusion := range []string{"STARTUP_FAILURE", "STALE"} {
		if got := ansi.Strip(checkSummary([]gh.PRCheck{{Status: "COMPLETED", Conclusion: conclusion}})); !strings.Contains(got, "failed") {
			t.Fatalf("%s summary = %q", conclusion, got)
		}
	}
}

func TestLocalPRListEntryCarriesDiffStats(t *testing.T) {
	m := testModel()
	m.localAvailable, m.localTitle = true, "local"
	m.currentBranch, m.defaultBranch = "feature", "main"
	m.localStats = git.ChangeStats{Files: 3, Additions: 20, Deletions: 4}
	m.localCommitCount = 2
	items := m.withLocalPR(nil)
	if len(items) != 1 || items[0].ChangedFiles != 3 || items[0].Additions != 20 || items[0].Deletions != 4 || items[0].CommitCount != 2 {
		t.Fatalf("local PR metadata = %#v", items)
	}
}

func TestPRViewOrderDefaultsToAssigned(t *testing.T) {
	var m Model
	if m.prView != assignedView {
		t.Fatalf("default view = %v", m.prView)
	}
	want := []prView{assignedView, reviewRequestedView, allPRsView, authoredView, needsMeView, closedPRsView}
	for i, view := range want {
		if prView(i) != view {
			t.Fatalf("view order[%d] = %v, want %v", i, prView(i), view)
		}
	}
}

func TestPRSavedViewsUseViewerMetadata(t *testing.T) {
	m := testModel()
	m.viewerLogin = "me"
	m.navigator.PRs = []gh.PR{
		{Number: 1, Author: gh.PRUser{Login: "me"}},
		{Number: 2, Assignees: []gh.PRUser{{Login: "me"}}},
		{Number: 3, ReviewRequests: []gh.PRUser{{Login: "me"}}},
		{Number: 4, Author: gh.PRUser{Login: "other"}},
	}
	want := map[prView][]int{
		allPRsView:          {1, 2, 3, 4},
		reviewRequestedView: {3},
		assignedView:        {2},
		authoredView:        {1},
		needsMeView:         {2, 3},
	}
	for view, numbers := range want {
		m.prView = view
		m.applyPRFilters(0)
		got := make([]int, len(m.openPRs))
		for i := range m.openPRs {
			got[i] = m.openPRs[i].Number
		}
		if !reflect.DeepEqual(got, numbers) {
			t.Fatalf("view %s = %v, want %v", view, got, numbers)
		}
	}
}

func TestReplacePRsForStatePreservesOtherStateWithoutDuplicates(t *testing.T) {
	existing := []gh.PR{{Number: 1, State: "OPEN"}, {Number: 2, State: "CLOSED"}}
	got := replacePRsForState(existing, []gh.PR{{Number: 3, State: "CLOSED"}}, closedPRListState)
	if len(got) != 2 || got[0].Number != 1 || got[1].Number != 3 {
		t.Fatalf("merged state cache = %#v", got)
	}
	got = replacePRsForState(existing, []gh.PR{{Number: 1, State: "CLOSED"}}, closedPRListState)
	if len(got) != 1 || got[0].Number != 1 || got[0].State != "CLOSED" {
		t.Fatalf("deduplicated state transition = %#v", got)
	}
}

func TestPRListLoadsClosedFromView(t *testing.T) {
	m := testModel()
	m.screen, m.viewerLogin = prListScreen, "me"
	m.navigator.FetchedStates = map[string]bool{"OPEN": true}
	m.navigator.PRs = []gh.PR{{Number: 1, State: "OPEN", Title: "open", Assignees: []gh.PRUser{{Login: "me"}}}}
	m.applyPRFilters(0)
	if len(m.openPRs) != 1 || m.openPRs[0].Number != 1 {
		t.Fatalf("default state = %#v", m.openPRs)
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	m.prView = needsMeView
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	m = u.(Model)
	if m.prView != closedPRsView || m.prListState != closedPRListState || !m.listRefreshing || len(m.openPRs) != 0 {
		t.Fatalf("closed view switch = view:%v state:%v refreshing:%v prs:%#v", m.prView, m.prListState, m.listRefreshing, m.openPRs)
	}
	u, _ = m.Update(prListRefreshed{generation: m.prListGeneration, state: closedPRListState, viewer: "me", prs: []gh.PR{{Number: 2, State: "CLOSED", Title: "closed"}}})
	m = u.(Model)
	m.sync()
	if len(m.openPRs) != 1 || m.openPRs[0].Number != 2 || len(m.navigator.PRs) != 2 || m.viewCount(assignedView) != 1 || !strings.Contains(ansi.Strip(m.buildPRList()), "closed") {
		t.Fatalf("closed PR list/cache = visible:%#v cached:%#v assigned:%d", m.openPRs, m.navigator.PRs, m.viewCount(assignedView))
	}
	if m.keys.Merge.Enabled() || m.keys.Close.Enabled() {
		t.Fatal("closed PR actions must be disabled")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	m = u.(Model)
	if m.prView != assignedView || m.listRefreshing || len(m.openPRs) != 1 || m.openPRs[0].Number != 1 {
		t.Fatalf("cached assigned view = view:%v refreshing:%v prs:%#v", m.prView, m.listRefreshing, m.openPRs)
	}
}

func TestPRListLoadsClosedFromSearch(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator.FetchedStates = map[string]bool{"OPEN": true}
	m.navigator.PRs = []gh.PR{{Number: 1, State: "OPEN", Title: "open"}}
	m.applyPRFilters(0)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("is:closed")})
	m = u.(Model)
	if m.prView != allPRsView || m.prListState != closedPRListState || !m.listRefreshing || len(m.openPRs) != 0 {
		t.Fatalf("closed search = view:%v state:%v refreshing:%v prs:%#v", m.prView, m.prListState, m.listRefreshing, m.openPRs)
	}
	u, _ = m.Update(prListRefreshed{generation: m.prListGeneration, state: closedPRListState, prs: []gh.PR{{Number: 2, State: "CLOSED", Title: "closed"}}})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if m.filterQuery != "" || m.prListState != openPRListState || m.listRefreshing || len(m.openPRs) != 1 || m.openPRs[0].Number != 1 {
		t.Fatalf("cleared closed search = query:%q state:%v refreshing:%v prs:%#v", m.filterQuery, m.prListState, m.listRefreshing, m.openPRs)
	}
}

func TestLazyPreviewKeepsCurrentPRSelection(t *testing.T) {
	m := testModel()
	m.screen, m.prListGeneration = prListScreen, 1
	m.navigator.PRs = []gh.PR{{Number: 1, State: "OPEN"}, {Number: 2, State: "OPEN"}}
	m.applyPRFilters(0)
	m.prCursor = 1
	u, _ := m.Update(prPreviewLoaded{generation: 1, number: 1, pr: gh.PR{Number: 1, State: "OPEN", PreviewLoaded: true}})
	m = u.(Model)
	if m.selectedPRNumber() != 2 {
		t.Fatalf("lazy preview moved selection to PR #%d", m.selectedPRNumber())
	}
}

func TestPRFilterSupportsGitHubTermsAndFreeText(t *testing.T) {
	failed := []gh.PRCheck{{Status: "COMPLETED", Conclusion: "FAILURE"}}
	pr := gh.PR{Number: 7, Title: "Fix Login", State: "OPEN", HeadRefName: "auth/fix", Author: gh.PRUser{Login: "alice"}, Assignees: []gh.PRUser{{Login: "me"}}, ReviewRequests: []gh.PRUser{{Login: "reviewer"}}, Labels: []gh.PRLabel{{Name: "bug"}}, Checks: failed, Mergeable: "CONFLICTING", IsDraft: false}
	for _, query := range []string{"login", "#7", "is:open", "state:open", "author:alice", "assignee:@me", "review-requested:reviewer", "label:bug", "draft:false", "ci:failed", "merge:conflicting", "label:bug ci:failed auth"} {
		if !matchesPRFilter(pr, query, "me") {
			t.Fatalf("filter %q did not match", query)
		}
	}
	teamRequest := pr
	teamRequest.ReviewRequests = nil
	teamRequest.ViewerReviewRequested = true
	if !matchesPRFilter(teamRequest, "review-requested:@me", "me") {
		t.Fatal("team review request did not match @me")
	}
	for _, query := range []string{"is:closed", "state:closed", "author:bob", "assignee:bob", "review-requested:@me", "label:docs", "draft:true", "ci:passed"} {
		if matchesPRFilter(pr, query, "me") {
			t.Fatalf("filter %q unexpectedly matched", query)
		}
	}
}

func TestPRListFilterEditingAndViewKeys(t *testing.T) {
	m := testModel()
	m.screen, m.viewerLogin = prListScreen, "me"
	m.navigator.PRs = []gh.PR{{Number: 1, Title: "Bug", Labels: []gh.PRLabel{{Name: "bug"}}}, {Number: 2, Title: "Feature", ReviewRequests: []gh.PRUser{{Login: "me"}}}}
	m.applyPRFilters(0)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	m.prCursor = 1
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("label:bug")})
	m = u.(Model)
	if !m.filterEditing || m.filterQuery != "label:bug" || len(m.openPRs) != 1 || m.openPRs[0].Number != 1 {
		t.Fatalf("live filter = editing:%v query:%q prs:%#v", m.filterEditing, m.filterQuery, m.openPRs)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if m.filterQuery != "" || len(m.openPRs) != 2 || m.selectedPRNumber() != 2 {
		t.Fatalf("Esc did not clear filter/restore selection: %q %#v selected=%d", m.filterQuery, m.openPRs, m.selectedPRNumber())
	}
	if m.help.Width != 120 {
		t.Fatalf("help width = %d", m.help.Width)
	}
	m.prView = assignedView
	m.applyPRFilters(0)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	m = u.(Model)
	if m.prView != reviewRequestedView || len(m.openPRs) != 1 || m.openPRs[0].Number != 2 {
		t.Fatalf("next view = %v %#v", m.prView, m.openPRs)
	}
	plain := ansi.Strip(m.renderPRListHeader())
	for _, want := range []string{"Assigned 0", "Review requested 1", "All 2", "Authored 0", "Needs me 1", "Closed 0"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("header missing %q: %q", want, plain)
		}
	}
}

func TestPRFilteringDoesNotMutateNavigatorCache(t *testing.T) {
	m := testModel()
	m.currentBranch, m.defaultBranch = "feature/local", "main"
	m.localAvailable = true
	m.navigator.PRs = []gh.PR{{Number: 1, HeadRefName: "one"}, {Number: 2, HeadRefName: "two"}}
	want := append([]gh.PR(nil), m.navigator.PRs...)
	m.applyPRFilters(0)
	if !reflect.DeepEqual(m.navigator.PRs, want) {
		t.Fatalf("display filtering mutated navigator cache: got=%#v want=%#v", m.navigator.PRs, want)
	}
}

func TestBuildPRStacksUsesExactBaseHeadGraph(t *testing.T) {
	prs := []gh.PR{
		{Number: 3, Title: "UI", BaseRefName: "stack/api", HeadRefName: "stack/ui"},
		{Number: 2, Title: "API", BaseRefName: "stack/model", HeadRefName: "stack/api"},
		{Number: 1, Title: "Model", BaseRefName: "main", HeadRefName: "stack/model"},
		{Number: 4, Title: "Independent", BaseRefName: "main", HeadRefName: "other"},
	}
	stacks := buildPRStacks(prs)
	if len(stacks) != 2 || len(stacks[0].entries) != 3 || len(stacks[1].entries) != 1 {
		t.Fatalf("stacks = %#v", stacks)
	}
	for i, want := range []int{1, 2, 3} {
		if stacks[0].entries[i].pr.Number != want || stacks[0].entries[i].depth != i {
			t.Fatalf("chain[%d] = %#v", i, stacks[0].entries[i])
		}
	}
	if stacks[1].entries[0].pr.Number != 4 {
		t.Fatalf("independent stack = %#v", stacks[1])
	}
}

func TestBuildPRStacksSupportsBranchesWithoutTitleHeuristics(t *testing.T) {
	prs := []gh.PR{
		{Number: 2, Title: "same", BaseRefName: "root", HeadRefName: "child-a"},
		{Number: 3, Title: "same", BaseRefName: "root", HeadRefName: "child-b"},
		{Number: 1, Title: "different", BaseRefName: "main", HeadRefName: "root"},
		{Number: 4, Title: "same", BaseRefName: "main", HeadRefName: "unrelated"},
	}
	stacks := buildPRStacks(prs)
	if len(stacks) != 2 || len(stacks[0].entries) != 3 || stacks[0].entries[1].depth != 1 || stacks[0].entries[2].depth != 1 || len(stacks[1].entries) != 1 {
		t.Fatalf("branched stacks = %#v", stacks)
	}
}

func TestDuplicateHeadBranchesDoNotInventStackParent(t *testing.T) {
	prs := []gh.PR{{Number: 1, HeadRefName: "same"}, {Number: 2, HeadRefName: "same"}, {Number: 3, BaseRefName: "same", HeadRefName: "child"}}
	stacks := buildPRStacks(prs)
	if len(stacks) != 3 {
		t.Fatalf("ambiguous head created stack: %#v", stacks)
	}
}

func TestPRStackRenderingAndCollapse(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator.PRs = []gh.PR{
		{Number: 3, Title: "UI", BaseRefName: "stack/api", HeadRefName: "stack/ui", MergeStateStatus: "DIRTY", Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}},
		{Number: 2, Title: "API", BaseRefName: "stack/model", HeadRefName: "stack/api", Checks: []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}},
		{Number: 1, Title: "Model", BaseRefName: "main", HeadRefName: "stack/model"},
	}
	m.applyPRFilters(3)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	plain := ansi.Strip(m.buildPRList())
	for _, want := range []string{"stack/model · 3 PRs", "├ #1", "├ #2", "└ #3"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("stack render missing %q: %q", want, plain)
		}
	}
	if !strings.Contains(plain, "CI 1 pending") || !strings.Contains(plain, "conflicts") {
		t.Fatalf("PR row state missing: %q", plain)
	}
	if strings.Contains(plain, "3 PRs ·") {
		t.Fatalf("stack header leaked aggregate PR state: %q", plain)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = u.(Model)
	if len(m.openPRs) != 1 || m.openPRs[0].Number != 1 || !m.collapsedStacks[m.prStacks[0].id] {
		t.Fatalf("collapsed stack = prs:%#v collapsed:%#v", m.openPRs, m.collapsedStacks)
	}
	if !strings.Contains(ansi.Strip(m.buildPRList()), "▸ stack/model") {
		t.Fatalf("collapsed header = %q", ansi.Strip(m.buildPRList()))
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = u.(Model)
	if len(m.openPRs) != 3 || m.collapsedStacks[m.prStacks[0].id] {
		t.Fatalf("expanded stack = prs:%#v collapsed:%#v", m.openPRs, m.collapsedStacks)
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

func TestStalePRListRefreshCannotRestoreMergedPR(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.prListGeneration = 2
	m.listRefreshing = true
	m.openPRs = []gh.PR{{Number: 2}}
	u, cmd := m.Update(prListRefreshed{generation: 1, prs: []gh.PR{{Number: 1}}})
	m = u.(Model)
	if cmd != nil || len(m.openPRs) != 1 || m.openPRs[0].Number != 2 || !m.listRefreshing {
		t.Fatalf("stale PR list applied: prs=%v refreshing=%v cmd=%v", m.openPRs, m.listRefreshing, cmd)
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

func TestPRListActionsRequireConfirmation(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.openPRs = []gh.PR{{Number: 14, HeadRefName: "feature", HeadRefOID: "abc123"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = u.(Model)
	if cmd != nil || m.pendingPRAction != mergePR || m.prActionPR.HeadRefOID != "abc123" || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Merge PR #14") || !strings.Contains(ansi.Strip(m.renderActionPopup()), "merge commit") || !strings.Contains(ansi.Strip(m.View()), "Merge PR #14") {
		t.Fatalf("merge confirmation not shown: pending=%v popup=%q", m.pendingPRAction, ansi.Strip(m.renderActionPopup()))
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = u.(Model)
	if m.pendingPRAction != noPRAction {
		t.Fatalf("merge confirmation not cancelled: %v", m.pendingPRAction)
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = u.(Model)
	if cmd == nil || m.pendingPRAction != noPRAction || m.prActionRunning != checkoutPR || m.prActionNumber != 14 {
		t.Fatalf("checkout not confirmed: pending=%v running=%v number=%d cmd=%v", m.pendingPRAction, m.prActionRunning, m.prActionNumber, cmd)
	}

	m.prActionRunning = noPRAction
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = u.(Model)
	if m.pendingPRAction != closePR || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Close PR #14") || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Close without merging") {
		t.Fatalf("close confirmation not shown: pending=%v popup=%q", m.pendingPRAction, ansi.Strip(m.renderActionPopup()))
	}
}

func TestPRListMergeCompletionRefreshesAndReportsErrors(t *testing.T) {
	m := testModel()
	m.screen, m.prActionRunning, m.prActionNumber = prListScreen, mergePR, 14
	u, cmd := m.Update(prActionDone{action: mergePR, number: 14})
	m = u.(Model)
	if cmd == nil || !m.listRefreshing || m.notice != "Merge submitted for PR #14" || m.prActionRunning != noPRAction {
		t.Fatalf("merge completion = refreshing:%v notice:%q running:%v cmd:%v", m.listRefreshing, m.notice, m.prActionRunning, cmd)
	}

	m.prActionRunning = mergePR
	u, cmd = m.Update(prActionDone{action: mergePR, number: 15, err: errors.New("blocked")})
	m = u.(Model)
	if cmd != nil || !strings.Contains(m.status, "PR #15") || !strings.Contains(m.status, "blocked") {
		t.Fatalf("merge error = status:%q cmd:%v", m.status, cmd)
	}
}

func TestPRListCloseCompletionRefreshes(t *testing.T) {
	m := testModel()
	m.screen, m.prActionRunning, m.prActionNumber = prListScreen, closePR, 14
	u, cmd := m.Update(prActionDone{action: closePR, number: 14})
	m = u.(Model)
	if cmd == nil || !m.listRefreshing || m.notice != "Closed PR #14" || m.prActionRunning != noPRAction {
		t.Fatalf("close completion = refreshing:%v notice:%q running:%v cmd:%v", m.listRefreshing, m.notice, m.prActionRunning, cmd)
	}
}

func TestCheckoutReloadEpochRejectsPreCheckoutMessages(t *testing.T) {
	old := testModel()
	old.targetGeneration = 7
	old.prListGeneration = 5
	next := testModel()
	next.advanceAsyncGenerations(old)
	next.cache.PR = &gh.PR{Number: 2}
	next.openPRs = []gh.PR{{Number: 2}}
	u, _ := next.Update(remoteLoaded{generation: 7, pr: gh.PR{Number: 1}})
	next = u.(Model)
	if next.cache.PR == nil || next.cache.PR.Number != 2 {
		t.Fatalf("pre-checkout remote result replaced target: %#v", next.cache.PR)
	}
	u, _ = next.Update(prListRefreshed{generation: 5, prs: []gh.PR{{Number: 1}}})
	next = u.(Model)
	if len(next.openPRs) != 1 || next.openPRs[0].Number != 2 {
		t.Fatalf("pre-checkout PR list result replaced list: %#v", next.openPRs)
	}
	next.diffCache = map[string]string{"same-range": "new branch"}
	u, _ = next.Update(diffRendered{generation: 7, key: "same-range", output: "old branch"})
	next = u.(Model)
	if next.diffCache["same-range"] != "new branch" {
		t.Fatalf("pre-checkout diff replaced new branch: %#v", next.diffCache)
	}
}

func TestExplicitForkCheckoutRemainsCurrentTarget(t *testing.T) {
	m := testModel()
	m.currentBranch = "fork-local"
	pr := gh.PR{Number: 12, HeadRefName: "fork-head", IsCrossRepository: true}
	m.cache.PR, m.cache.ExplicitCheckout = &pr, true
	m.localAvailable = true
	if !m.isCurrentTargetPR(pr) {
		t.Fatal("explicitly checked-out fork is not the current target")
	}
	items := m.withLocalPR([]gh.PR{pr})
	if len(items) != 1 || items[0].Number != 12 {
		t.Fatalf("explicit fork duplicated as local PR: %#v", items)
	}
	m.screen, m.openPRs = prListScreen, items
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	if m.keys.Checkout.Enabled() {
		t.Fatal("checkout remained enabled for explicit fork target")
	}
}

func TestPRListActionsAreDisabledForLocalEntry(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.openPRs = []gh.PR{{Title: "Local PR"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	for _, action := range []rune{'m', 'c', 'x'} {
		u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{action}})
		m = u.(Model)
		if cmd != nil || m.pendingPRAction != noPRAction {
			t.Fatalf("local action %q was enabled", action)
		}
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
	m.navigator.PRs = append([]gh.PR(nil), m.openPRs...)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	generationA := m.targetGeneration
	m.autoOpenCurrent = true
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = u.(Model)
	if m.autoOpenCurrent {
		t.Fatal("explicit PR-list navigation must disable startup auto-open")
	}
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
	m.currentBranch, m.defaultBranch = "feature", "main"
	m.localAvailable, m.localTitle = true, "local work"
	m.openPRs = m.withLocalPR(nil)
	m.focusDiff = true
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = u.(Model)
	if m.screen != prListScreen || m.diffTerminal != nil || !strings.Contains(ansi.Strip(m.View()), "Local PR") {
		t.Fatalf("b did not return to local PR list: screen=%v terminal=%v view=%q", m.screen, m.diffTerminal, ansi.Strip(m.View()))
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
