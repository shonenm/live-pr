package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prfilter"
	"github.com/shonenm/live-pr/internal/theme"
)

func TestModelDoesNotConfigureARealReviewerProcess(t *testing.T) {
	m := testModel()
	if m.diffCommand != "" {
		t.Fatalf("test model reviewer command = %q; tests must opt in to child processes", m.diffCommand)
	}
	if terminal := embeddedterm.New(m.diffCommand, t.TempDir(), nil); terminal != nil {
		t.Fatal("empty test reviewer command created a terminal")
	}
}

func TestReviewRangesUseLocalMergeBaseAndRemoteHistoricalBase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = merge-base ]; then echo local-merge-base; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := localReviewBase("main", nil); got != "local-merge-base" {
		t.Fatalf("local review base = %q", got)
	}
	pr := gh.PR{BaseRefName: "main", BaseRefOID: "historical-base"}
	if got := remoteReviewBase(pr); got != "historical-base" {
		t.Fatalf("remote review base = %q", got)
	}
}

func TestPublishedCheckoutUsesGitHubHeadNotWorktree(t *testing.T) {
	pr := &gh.PR{Number: 12, BaseRefOID: "baseoid", HeadRefOID: "pushedhead"}
	diffBase, headRev, reviewRange := localReviewRange("main", pr, "HEAD", false)
	if diffBase != "baseoid" || headRev != "pushedhead" || reviewRange != "baseoid...pushedhead" {
		t.Fatalf("published range = %q %q %q", diffBase, headRev, reviewRange)
	}
	diffBase, headRev, reviewRange = localReviewRange("main", nil, "HEAD", false)
	if headRev != "HEAD" || reviewRange != diffBase {
		t.Fatalf("unpublished local range = %q %q %q", diffBase, headRev, reviewRange)
	}
}

func TestLayoutWaitsForTerminalSize(t *testing.T) {
	m := testModel()
	m.ready, m.w, m.h = false, 0, 0
	m.layout()
	if m.ready || m.list.Width != 0 || m.detail.Width != 0 {
		t.Fatalf("layout initialized before terminal size: ready=%v list=%dx%d detail=%dx%d", m.ready, m.list.Width, m.list.Height, m.detail.Width, m.detail.Height)
	}
	if got := m.View(); got != "loading…" {
		t.Fatalf("pre-size view = %q", got)
	}
}

func TestQuarterViewportScroll(t *testing.T) {
	v := viewport.New(40, 20)
	v.SetContent(strings.Repeat("line\n", 100))
	scrollQuarter(&v, true)
	if v.YOffset != 5 {
		t.Fatalf("quarter down offset = %d, want 5", v.YOffset)
	}
	scrollQuarter(&v, false)
	if v.YOffset != 0 {
		t.Fatalf("quarter up offset = %d, want 0", v.YOffset)
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

func TestHeaderShowsPRStatusSizeAndLocalChanges(t *testing.T) {
	m := testModel()
	m.w = 180
	m.cache.PR = &gh.PR{
		Number:         12,
		State:          "OPEN",
		ReviewDecision: "APPROVED",
		Checks: []gh.PRCheck{
			{Status: "IN_PROGRESS"},
			{Status: "COMPLETED", Conclusion: "FAILURE"},
			{Status: "COMPLETED", Conclusion: "FAILURE"},
			{Status: "COMPLETED", Conclusion: "SUCCESS"},
			{Status: "COMPLETED", Conclusion: "SUCCESS"},
			{Status: "COMPLETED", Conclusion: "SUCCESS"},
		},
		PreviewLoaded:  true,
		Assignees:      []gh.PRUser{{Login: "alice"}, {Login: "bob"}},
		ReviewRequests: []gh.PRUser{{Login: "carol"}},
		Labels:         []gh.PRLabel{{Name: "bug", Color: "d73a4a"}, {Name: "docs", Color: "fef2c0"}},
	}
	m.localStats = git.ChangeStats{Files: 4, Additions: 20, Deletions: 3}
	m.workingTreeDirty = true
	plain := ansi.Strip(m.renderHeader())
	for _, want := range []string{"#12 open", "CI 1/2/3", "review approved", "4 files", "+20", "-3", "uncommitted changes", "● @alice ● @bob", "review requested ● @carol", "bug", "docs"} {
		if strings.Contains(plain, "passed") || strings.Contains(plain, "failed") || strings.Contains(plain, "pending") {
			t.Fatalf("header CI still has status words: %q", plain)
		}
		if !strings.Contains(plain, want) {
			t.Fatalf("header missing %q: %q", want, plain)
		}
	}
	for _, unwanted := range []string{" commits ·", " events ·", " comments ·", " activity ·"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("header still contains Conversation count %q: %q", unwanted, plain)
		}
	}
	if m.headerHeight() != logoHeight {
		t.Fatalf("PR header height = %d", m.headerHeight())
	}
	if lines := strings.Count(plain, "\n") + 1; lines != logoHeight {
		t.Fatalf("PR header rendered %d lines, want %d: %q", lines, logoHeight, plain)
	}
	m.remote = true
	m.cache.PR.ChangedFiles, m.cache.PR.Additions, m.cache.PR.Deletions = 7, 30, 4
	remote := ansi.Strip(m.renderHeader())
	if strings.Contains(remote, "uncommitted changes") {
		t.Fatalf("remote PR header exposed local worktree state: %q", remote)
	}
	for _, want := range []string{"7 files", "+30", "-4"} {
		if !strings.Contains(remote, want) {
			t.Fatalf("remote PR header missing %q: %q", want, remote)
		}
	}
	m.remote = false
	m.w = 25
	if width := lipgloss.Width(m.renderPRMeta(*m.cache.PR)); width > m.w {
		t.Fatalf("metadata width = %d, want <= %d", width, m.w)
	}
	m.cache.PR = nil
	if m.headerHeight() != logoHeight {
		t.Fatalf("local header height = %d", m.headerHeight())
	}
}

func TestHeaderListsClosingIssues(t *testing.T) {
	m := testModel()
	m.w = 180
	m.cache.PR = &gh.PR{Number: 12, State: "OPEN", ClosingIssues: []gh.IssueRef{{Number: 34, Title: "Crash"}, {Number: 56, Title: "Typo"}}}
	plain := ansi.Strip(m.renderHeader())
	if !strings.Contains(plain, "⊙ closes #34, #56") {
		t.Fatalf("header missing linked issues: %q", plain)
	}
	if lines := strings.Count(plain, "\n") + 1; lines != logoHeight {
		t.Fatalf("closing issues grew the header to %d lines, want %d", lines, logoHeight)
	}
	m.cache.PR.ClosingIssues = nil
	if plain := ansi.Strip(m.renderHeader()); strings.Contains(plain, "closes #") {
		t.Fatalf("header shows closing issues without any: %q", plain)
	}
}

func TestHeaderShowsPendingReviewDraft(t *testing.T) {
	m := testModel()
	m.w = 180
	m.cache.PR = &gh.PR{Number: 12, State: "OPEN", HeadRefOID: "abc"}
	if plain := ansi.Strip(m.renderHeader()); strings.Contains(plain, "review draft") {
		t.Fatalf("header shows a draft badge without a draft: %q", plain)
	}
	m.reviewDraft = gh.NewReviewDraft(12, "abc")
	if plain := ansi.Strip(m.renderHeader()); strings.Contains(plain, "review draft") {
		t.Fatalf("header shows a draft badge for an empty draft: %q", plain)
	}
	m.reviewDraft.Comments = []gh.ReviewComment{
		{Path: "a.go", Line: 1, Side: "RIGHT", Body: "x"},
		{Path: "b.go", Line: 2, Side: "RIGHT", Body: "y"},
	}
	if plain := ansi.Strip(m.renderHeader()); !strings.Contains(plain, "✎ review draft · 2 comments") {
		t.Fatalf("header missing the pending draft badge: %q", plain)
	}
	m.reviewDraft.Comments = nil
	m.reviewDraft.Body = "overall verdict"
	plain := ansi.Strip(m.renderHeader())
	if !strings.Contains(plain, "✎ review draft") || strings.Contains(plain, "review draft ·") {
		t.Fatalf("body-only draft badge should not count comments: %q", plain)
	}
}

func TestHeaderCarriesWordmarkAndVersionUntilTheTerminalIsNarrow(t *testing.T) {
	m := testModel()
	m.version = "0.2.4"
	m.w = 120
	wide := ansi.Strip(m.renderHeader())
	if !strings.Contains(wide, "┗━╸╹┗┛ ┗━╸") || !strings.Contains(wide, "v0.2.4") {
		t.Fatalf("wide header missing the wordmark or version: %q", wide)
	}
	if first := strings.Split(wide, "\n")[0]; !strings.HasSuffix(strings.TrimRight(first, " "), "v0.2.4") {
		t.Fatalf("version is not pinned to the top-right: %q", first)
	}
	for _, line := range strings.Split(wide, "\n") {
		if width := lipgloss.Width(line); width > m.w {
			t.Fatalf("header line overflows %d: %d %q", m.w, width, line)
		}
	}
	m.w = logoWidth + 10
	narrow := ansi.Strip(m.renderHeader())
	if strings.Contains(narrow, "┗━╸╹┗┛ ┗━╸") {
		t.Fatalf("narrow header should drop the wordmark: %q", narrow)
	}
	if lines := strings.Count(narrow, "\n") + 1; lines != logoHeight {
		t.Fatalf("narrow header = %d lines, want %d to keep the layout stable", lines, logoHeight)
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

func TestApplyThemeRebuildsSemanticStyles(t *testing.T) {
	defer applyTheme(theme.PrimerDark())
	applyTheme(theme.ByName("primer-light"))
	if cFg != "#1f2328" || cOpen != "#1f883d" || cDoneEmphasis != "#8250df" {
		t.Fatalf("primer-light palette = fg %s, open %s, done %s", cFg, cOpen, cDoneEmphasis)
	}
	if got := stFg.GetForeground(); got != lipgloss.Color("#1f2328") {
		t.Fatalf("stFg foreground = %v, want the primer-light foreground", got)
	}
}

// TestEmphasisInkKeepsPrimerDarkInks pins the filled-block text colors the
// default theme has always used: GitHub's dark ink on the accent footer,
// white on every state badge.
func TestEmphasisInkKeepsPrimerDarkInks(t *testing.T) {
	if got := emphasisInk(cAccent); got != "#0d1117" {
		t.Fatalf("footer ink = %s, want #0d1117", got)
	}
	for _, background := range []string{cOpen, cDoneEmphasis, cDangerEmphasis, cClosed, cBorder} {
		if got := emphasisInk(background); got != "#ffffff" {
			t.Fatalf("badge ink on %s = %s, want #ffffff", background, got)
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

func TestRelativeTS(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, ts, want string
	}{
		{name: "just now", ts: "2026-08-19T11:59:30Z", want: "just now"},
		{name: "minutes", ts: "2026-08-19T11:15:00Z", want: "45m ago"},
		{name: "hours", ts: "2026-08-19T07:00:00Z", want: "5h ago"},
		{name: "days", ts: "2026-08-16T12:00:00Z", want: "3d ago"},
		{name: "same year date", ts: "2026-03-05T12:00:00Z", want: "Mar 5"},
		{name: "older year date", ts: "2025-01-02T12:00:00Z", want: "Jan 2, 2025"},
		{name: "unparseable falls back", ts: "not-a-timestamp", want: "not-a-timestamp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeTS(now, tc.ts); got != tc.want {
				t.Fatalf("relativeTS(%q) = %q, want %q", tc.ts, got, tc.want)
			}
		})
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
	m.screen, m.prList.open = prListScreen, items
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	if m.keys.Checkout.Enabled() {
		t.Fatal("checkout remained enabled for explicit fork target")
	}
}

func TestPRTitleAndCurrentCheckoutMarker(t *testing.T) {
	m := testModel()
	m.w, m.list.Width = 160, 120
	m.currentBranch = "feature"
	pr := gh.PR{Number: 12, State: "OPEN", Title: "Human title", HeadRefName: "feature", BaseRefName: "main"}
	m.cache.PR = &pr
	m.detailView.title = "feature"
	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(header, "Human title") || strings.Contains(header, "  feature\n") {
		t.Fatalf("detail title = %q", header)
	}
	// Selection is shown by the row background (not visible after ansi.Strip);
	// the current-checkout PR still keeps its ▌ marker.
	plain := ansi.Strip(strings.Join(m.renderPRRow(pr, true, ""), "\n"))
	if !strings.Contains(plain, "▌") {
		t.Fatalf("current-checkout marker missing: %q", plain)
	}
	// An unselected, non-current PR shows no bar at all.
	other := gh.PR{Number: 13, State: "OPEN", Title: "Other", HeadRefName: "other", BaseRefName: "main"}
	if plainOther := ansi.Strip(strings.Join(m.renderPRRow(other, false, ""), "\n")); strings.Contains(plainOther, "▌") {
		t.Fatalf("unexpected marker on plain row: %q", plainOther)
	}
}

func TestViewRendersHeaderAndTimeline(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	out := m.View()
	for _, want := range []string{"Local", "main", "feature/x", "1 files", "decision", "chose Go"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestPRRefreshAddsMetadataRowWithinTheHeader(t *testing.T) {
	m := testModel()
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	before := m.detail.Height
	u, _ = m.Update(githubRefreshed{pr: gh.PR{Number: 12, State: "OPEN"}})
	m = u.(Model)
	// The wordmark already reserves three header rows, so the metadata row
	// costs no body height.
	if m.detail.Height != before || m.headerHeight() != logoHeight {
		t.Fatalf("metadata row disturbed the layout: before=%d after=%d header=%d", before, m.detail.Height, m.headerHeight())
	}
	if !strings.Contains(ansi.Strip(m.renderHeader()), "#12 open") {
		t.Fatalf("metadata row missing from the header: %q", ansi.Strip(m.renderHeader()))
	}
}

func TestMultiplePRsSameBranchSelectByNumber(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "feature/x"
	pr10 := gh.PR{Number: 10, State: "CLOSED", HeadRefName: "feature/x", BaseRefName: "main", URL: "u10"}
	pr20 := gh.PR{Number: 20, State: "OPEN", HeadRefName: "feature/x", BaseRefName: "main", URL: "u20"}
	m.cache.PR = &pr10 // already loaded PR #10

	// PR #10 is already loaded → is current target.
	if !m.isCurrentTargetPR(pr10) {
		t.Fatal("PR #10 should be the current target")
	}
	// PR #20 shares the same branch but has a different number → not current target.
	if m.isCurrentTargetPR(pr20) {
		t.Fatal("PR #20 should NOT be the current target when #10 is loaded")
	}
}

func TestBackgroundSyncKeepsPreviewScroll(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator = gh.NewNavigatorCache()
	m.prList.open = []gh.PR{
		{Number: 1, State: "OPEN", Title: "one", Body: strings.Repeat("line\n", 80)},
		{Number: 2, State: "OPEN", Title: "two"},
	}
	m.prList.cursor = 0
	u, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 8})
	m = u.(Model)
	m.detail.SetYOffset(1)
	// A re-sync with the same selection (background preview/avatar arrivals)
	// must not reset the preview scroll.
	m.sync()
	if m.detail.YOffset == 0 {
		t.Fatal("background sync reset the preview scroll")
	}
	// Changing the selection resets it.
	m.prList.cursor = 1
	m.sync()
	if m.detail.YOffset != 0 {
		t.Fatalf("selection change kept stale scroll offset %d", m.detail.YOffset)
	}
}

func TestRichContentKeyIncludesWidthAndSkipsZeroWidth(t *testing.T) {
	pr := &gh.PR{Number: 1, Body: "```mermaid\ngraph TD;A-->B;\n```"}
	if richContentKey(80, pr, nil, nil) == richContentKey(40, pr, nil, nil) {
		t.Fatal("resize does not invalidate rendered mermaid")
	}
	m := testModel()
	m.cache.PR = pr
	m.list.Width = 0 // pre-layout: width-7 is negative
	if m.dispatchRichContent() != nil {
		t.Fatal("zero/negative width still dispatched a render")
	}
	m.list.Width = 87
	if m.dispatchRichContent() == nil {
		t.Fatal("first dispatch skipped")
	}
	// Same content and width: no re-render, no avatar re-download.
	if m.dispatchRichContent() != nil {
		t.Fatal("unchanged content dispatched again")
	}
	m.list.Width = 47
	if m.dispatchRichContent() == nil {
		t.Fatal("width change did not re-dispatch")
	}
}

func TestStaleGenerationRichContentWithMatchingKeyStillApplies(t *testing.T) {
	m := testModel()
	pr := &gh.PR{Number: 1, Body: "```mermaid\ngraph TD;A-->B;\n```"}
	pr.Author.Login = "alice"
	m.cache.PR = pr
	m.list.Width = 87
	if m.dispatchRichContent() == nil {
		t.Fatal("first dispatch skipped")
	}
	// A refresh mid-render bumps the generation while the content and width
	// stay the same: nothing resets lastRichContentKey, so if the in-flight
	// result were discarded, dispatchRichContent would return nil forever and the
	// mermaid diagrams and avatar colors would never render.
	m.targetGeneration++
	key := richContentKey(m.list.Width-7, m.cache.PR, m.cache.Comments, m.cache.Activities)
	u, _ := m.Update(richBodiesLoaded{key: key, bodies: map[string]string{pr.Body: "rendered"}})
	m = u.(Model)
	if m.detailView.richBodies[pr.Body] != "rendered" {
		t.Fatal("stale-generation bodies with a matching key were discarded")
	}
	u, _ = m.Update(avatarColorsLoaded{key: key, colors: map[string]string{"alice": "#ff0000"}})
	m = u.(Model)
	if m.avatarColors["alice"] != "#ff0000" {
		t.Fatal("stale-generation avatar colors with a matching key were discarded")
	}
	// A result rendered for other content or width still drops.
	u, _ = m.Update(richBodiesLoaded{key: richContentKey(1, pr, nil, nil), bodies: map[string]string{"other": "x"}})
	m = u.(Model)
	if _, ok := m.detailView.richBodies["other"]; ok {
		t.Fatal("mismatched-key result was applied")
	}
}

// A notice must not outlive the next thing the user does, or the footer keeps
// reporting an old action while newer work runs unannounced.
func TestNoticeClearsOnTheNextKeyAndRefreshAlwaysReports(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator = gh.NewNavigatorCache()
	pr := gh.PR{Number: 12, State: "OPEN", Title: "x"}
	m.prList.open = []gh.PR{pr}
	m.prList.stacks = prfilter.BuildStacks(m.prList.open)

	u, _ := m.Update(prStatusDone{pr: pr, target: "open"})
	m = u.(Model)
	if m.notice == "" {
		t.Fatal("setup: expected a notice from the status change")
	}
	// Any key retires it, not just refresh.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if got := u.(Model).notice; got != "" {
		t.Fatalf("notice survived a keypress: %q", got)
	}

	// r while a refresh is already running reports it rather than doing
	// nothing at all.
	m.notice = "URL copied to clipboard"
	m.prList.refreshing = true
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	busy := u.(Model)
	if busy.notice != "" {
		t.Fatalf("notice survived refresh: %q", busy.notice)
	}
	if !strings.Contains(ansi.Strip(busy.footerContent()), "fetching") {
		t.Fatalf("busy refresh gave no feedback: %q", ansi.Strip(busy.footerContent()))
	}

	// Same on the detail screen.
	d := testModel()
	d.screen, d.refreshing = detailScreen, true
	d.cache.PR = &pr
	d.notice = "Checked out PR #12"
	u, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	detail := u.(Model)
	if detail.notice != "" || !strings.Contains(ansi.Strip(detail.footerContent()), "refreshing") {
		t.Fatalf("detail refresh feedback = notice:%q footer:%q", detail.notice, ansi.Strip(detail.footerContent()))
	}
}

func TestNoticeYieldsToProgressAndClearsOnRefresh(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.notice = "Checked out PR #7"
	m.githubStatus = "GitHub: refreshing…"

	// Idle: the notice shows.
	if got := ansi.Strip(m.footerContent()); !strings.Contains(got, "Checked out PR #7") {
		t.Fatalf("idle footer lost the notice: %q", got)
	}
	// Loading: the progress line wins over the lingering notice.
	m.refreshing = true
	if got := ansi.Strip(m.footerContent()); strings.Contains(got, "Checked out PR #7") || !strings.Contains(got, "refreshing") {
		t.Fatalf("loading footer hid the progress: %q", got)
	}
	m.refreshing = false

	// r clears the notice on both screens.
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if got := u.(Model); got.notice != "" {
		t.Fatalf("detail refresh kept notice %q", got.notice)
	}
	list := testModel()
	list.screen = prListScreen
	list.notice = "Merge submitted for PR #7"
	u, _ = list.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if got := u.(Model); got.notice != "" {
		t.Fatalf("list refresh kept notice %q", got.notice)
	}
}

func TestBrowseURLAvailableOnEveryDetailTab(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	pr := gh.PR{Number: 7, URL: "https://example/pr/7"}
	m.cache.PR = &pr
	m.detailView.conversationDirty = true

	// c, f, i have no per-row link here (no checks are loaded), so they
	// offer the pull request itself.
	for _, tab := range []tab{commitsTab, conflictsTab, checksTab} {
		m.detailView.active = tab
		if got := m.selectedBrowseURL(); got != "https://example/pr/7" {
			t.Fatalf("tab %d URL = %q", tab, got)
		}
	}

	// A conversation row with its own URL still wins.
	comment := gh.Comment{ID: 1, Body: "hi", HTMLURL: "https://example/pr/7#c1"}
	comment.User.Login = "me"
	m.cache.Comments = []gh.Comment{comment}
	m.detailView.active = conversationTab
	m.detailView.conversationDirty = true
	items := m.conversationItems()
	for i, it := range items {
		if it.comment != nil {
			m.detailView.cursors[conversationTab] = i
		}
	}
	if got := m.selectedBrowseURL(); got != "https://example/pr/7#c1" {
		t.Fatalf("comment URL = %q", got)
	}
	// A row without one falls back rather than disabling the key.
	for i, it := range items {
		if it.activity != nil || it.event != nil {
			m.detailView.cursors[conversationTab] = i
			if got := m.selectedBrowseURL(); got != "https://example/pr/7" {
				t.Fatalf("row %d URL = %q", i, got)
			}
			break
		}
	}

	// Without a pull request there is still nothing to open.
	local := testModel()
	local.screen, local.detailView.active = detailScreen, checksTab
	if got := local.selectedBrowseURL(); got != "" {
		t.Fatalf("local-only detail URL = %q", got)
	}
}

func TestChecksTabBrowseURLOpensSelectedCheck(t *testing.T) {
	m := testModel()
	m.screen, m.detailView.active = detailScreen, checksTab
	m.cache.PR = &gh.PR{Number: 7, URL: "https://example/pr/7", Checks: []gh.PRCheck{
		{Name: "test", Conclusion: "FAILURE", DetailsURL: "https://example/runs/1"},
		{Context: "ci/legacy", State: "FAILURE", TargetURL: "https://example/status/2"},
		{Name: "pending", Status: "QUEUED"},
	}}

	// The selected row opens its own log page: detailsUrl for a check run,
	// targetUrl for a legacy status context.
	m.detailView.cursors[checksTab] = 0
	if got := m.selectedBrowseURL(); got != "https://example/runs/1" {
		t.Fatalf("check run URL = %q", got)
	}
	m.detailView.cursors[checksTab] = 1
	if got := m.selectedBrowseURL(); got != "https://example/status/2" {
		t.Fatalf("status context URL = %q", got)
	}
	// A check without a log page falls back to the pull request itself.
	m.detailView.cursors[checksTab] = 2
	if got := m.selectedBrowseURL(); got != "https://example/pr/7" {
		t.Fatalf("URL-less check fell back to %q", got)
	}
}

func TestDefaultBaseBranchIsAccented(t *testing.T) {
	m := testModel()
	m.defaultBranch = "main"

	// origin/ prefixed local revisions still name the default branch.
	for _, ref := range []string{"main", "origin/main", "MAIN"} {
		if !m.isDefaultBranch(ref) {
			t.Fatalf("%q not recognized as the default branch", ref)
		}
	}
	for _, ref := range []string{"", "feat/x", "mainline"} {
		if m.isDefaultBranch(ref) {
			t.Fatalf("%q wrongly treated as the default branch", ref)
		}
	}
	// Rendering in tests strips color, so assert on the style itself.
	if got := m.baseBranchStyle("origin/main").GetForeground(); got != lipgloss.Color(cAccent) {
		t.Fatalf("default base foreground = %v, want the accent", got)
	}
	if got := m.baseBranchStyle("feat/parent").GetForeground(); got == lipgloss.Color(cAccent) {
		t.Fatal("a stacked base must not use the accent")
	}
	empty := testModel()
	if got := empty.baseBranchStyle("main").GetForeground(); got == lipgloss.Color(cAccent) {
		t.Fatal("unknown default branch must not accent anything")
	}

	// Both surfaces keep showing base ← head.
	m.screen, m.w, m.h = detailScreen, 120, 40
	m.detailView.base, m.detailView.head = "origin/main", "feat/x"
	if header := ansi.Strip(m.renderHeader()); !strings.Contains(header, "origin/main ← feat/x") {
		t.Fatalf("detail header = %q", header)
	}
	list := testModel()
	list.defaultBranch = "main"
	list.screen, list.w = prListScreen, 120
	list.prList.open = []gh.PR{{Number: 1, Title: "one", BaseRefName: "main", HeadRefName: "feat/x", URL: "u"}}
	list.prList.stacks = prfilter.BuildStacks(list.prList.open)
	if preview := ansi.Strip(list.buildPRPreview()); !strings.Contains(preview, "main ← feat/x") {
		t.Fatalf("preview = %q", preview)
	}
}

// Bodies without mermaid used to be stored anyway, keyed by their own full
// text: every PR description and comment accumulated in richBodies for no
// rendering gain. richBody falls back to the raw body on a miss, so only
// rewritten bodies belong in the map.
func TestLoadRichContentSkipsBodiesWithoutMermaid(t *testing.T) {
	pr := &gh.PR{Number: 1, Body: "plain description, no diagrams"}
	comments := []gh.Comment{{Body: "plain comment"}}
	// resolved covers the only avatar login (""), so the avatar half of the
	// batch has nothing to download.
	cmd := loadRichContent(80, pr, comments, nil, map[string]bool{"": true})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("loadRichContent did not batch: %T", cmd())
	}
	for _, sub := range batch {
		msg, ok := sub().(richBodiesLoaded)
		if !ok {
			continue
		}
		if len(msg.bodies) != 0 {
			t.Fatalf("plain bodies were cached: %#v", msg.bodies)
		}
		return
	}
	t.Fatal("no richBodiesLoaded message dispatched")
}
