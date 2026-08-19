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
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/store"
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
		prView:      allPRsView,
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

	// A cache miss dispatches the git work as a Cmd instead of blocking.
	first, cmd := m.loadDetail()
	if cmd == nil || first.renderable {
		t.Fatalf("cache miss did not dispatch: %#v cmd=%v", first, cmd)
	}
	if again, dup := m.loadDetail(); dup != nil || again.renderable {
		t.Fatalf("pending key dispatched twice: %#v cmd=%v", again, dup)
	}
	u, _ := m.Update(cmd().(rawDetailLoaded))
	m = u.(Model)
	second, hitCmd := m.loadDetail()
	calls, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if second.raw != "cached diff" || hitCmd != nil || string(calls) != "1" {
		t.Fatalf("detail = %#v cmd=%v calls=%q", second, hitCmd, calls)
	}
	m.resetDetailCaches()
	_, missCmd := m.loadDetail()
	if missCmd == nil {
		t.Fatal("cache reset did not re-dispatch")
	}
	_ = missCmd()
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
	if !m.fileChecked(m.files[m.fileCursor]) {
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
	// q from the explorer should quit (Tab cycles focus instead).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q from explorer should quit")
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

// A PR closed on GitHub used to sit in the open list forever: the list keeps
// injecting the checked-out branch's PR, whose cached state only refreshed on
// the detail screen.
// The CI poll is the only thing that runs while live-pr sits idle. It used to
// push its cached copy of the PR back into the list wholesale, reverting a
// merged row to open until the next manual reload.
// Merge and close used to only refetch, and GitHub often still answers with
// the old state right afterwards — so nothing visibly changed until a manual
// reload. The outcome now lands immediately, everywhere the PR is shown.
func TestMergeAndCloseApplyWithoutAReload(t *testing.T) {
	pr := gh.PR{Number: 12, State: "OPEN", HeadRefName: "feature", Title: "x"}

	// List screen: the merged PR leaves the open page at once.
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, allPRsView, openPRListState
	m.navigator = gh.NewNavigatorCache()
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.prPages = map[string]prPageState{m.activePRPage: {prs: []gh.PR{pr}, total: 1, loaded: true, fresh: true}}
	m.navigator.PRs = []gh.PR{pr}
	u, _ := m.Update(prActionDone{action: mergePR, pr: pr, number: 12})
	m = u.(Model)
	for _, listed := range m.openPRs {
		if listed.Number == 12 {
			t.Fatalf("merged PR still on the open list: %#v", m.openPRs)
		}
	}
	if m.navigator.PRs[0].State != "MERGED" {
		t.Fatalf("navigator state = %q", m.navigator.PRs[0].State)
	}

	// Detail screen: the header state flips without leaving the screen.
	d := testModel()
	d.screen = detailScreen
	shown := pr
	d.cache = gh.NewCache("feature")
	d.cache.PR = &shown
	u, _ = d.Update(prActionDone{action: closePR, pr: shown, number: 12})
	if got := u.(Model).cache.PR.State; got != "CLOSED" {
		t.Fatalf("detail state after close = %q", got)
	}
}

func TestCIPollDoesNotRevertAMergedPR(t *testing.T) {
	m := testModel()
	m.screen, m.active = detailScreen, conversationTab
	m.currentBranch = "feature"
	m.navigator = gh.NewNavigatorCache()
	stale := gh.PR{Number: 12, State: "OPEN", HeadRefName: "feature", HeadRefOID: "abc",
		Checks: []gh.PRCheck{{Name: "test", Status: "IN_PROGRESS"}}, PreviewLoaded: true}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &stale
	merged := stale
	merged.State = "MERGED"
	m.navigator.PRs = []gh.PR{merged}

	u, cmd := m.Update(ciPolled{generation: m.targetGeneration, pr: gh.PR{
		Number: 12, State: "MERGED", HeadRefOID: "abc",
		Checks: []gh.PRCheck{{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}},
	}})
	m = u.(Model)
	if m.navigator.PRs[0].State != "MERGED" || m.cache.PR.State != "MERGED" {
		t.Fatalf("poll reverted the state: navigator=%q cache=%q", m.navigator.PRs[0].State, m.cache.PR.State)
	}
	if len(m.cache.PR.Checks) != 1 || m.cache.PR.Checks[0].Conclusion != "SUCCESS" {
		t.Fatalf("poll dropped the checks it fetched: %#v", m.cache.PR.Checks)
	}
	// Nothing left to watch, so the poll stops rather than looping forever.
	if cmd != nil && pollableCI(*m.cache.PR) {
		t.Fatal("kept polling a merged PR")
	}
	if pollableCI(gh.PR{State: "CLOSED", Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}}) {
		t.Fatal("a closed PR is still considered pollable")
	}
	if !pollableCI(gh.PR{State: "OPEN", Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}}) {
		t.Fatal("an open PR with pending checks must stay pollable")
	}
}

func TestClosedBranchPRLeavesTheOpenList(t *testing.T) {
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, allPRsView, openPRListState
	m.currentBranch, m.viewerLogin = "feature", "me"
	m.navigator = gh.NewNavigatorCache()
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	stale := gh.PR{Number: 1, State: "OPEN", HeadRefName: "feature", Title: "closed upstream"}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &stale
	m.prPages = map[string]prPageState{m.activePRPage: {
		prs: []gh.PR{{Number: 2, State: "OPEN", Title: "other"}}, total: 1, loaded: true, fresh: true,
	}}
	m.applyPRFilters(0)
	if len(m.openPRs) != 2 {
		t.Fatalf("setup: expected the stale PR to be listed, got %#v", m.openPRs)
	}

	// The refresh-triggered lookup reports it closed: it leaves the open list
	// without dragging the user to the closed tab or moving the selection.
	m.prCursor = 1
	u, _ := m.Update(currentBranchPRLoaded{pr: gh.PR{Number: 1, State: "CLOSED", HeadRefName: "feature"}, stateOnly: true})
	m = u.(Model)
	if m.prView != allPRsView {
		t.Fatalf("refresh switched to view %d", m.prView)
	}
	for _, pr := range m.openPRs {
		if pr.Number == 1 {
			t.Fatalf("closed PR still listed: %#v", m.openPRs)
		}
	}
	if m.cache.PR.State != "CLOSED" {
		t.Fatalf("cached state = %q", m.cache.PR.State)
	}

	// A list page carrying a newer state reconciles the cache the same way.
	reopened := testModel()
	reopened.screen, reopened.prView, reopened.prListState = prListScreen, allPRsView, openPRListState
	reopened.currentBranch = "feature"
	reopened.navigator = gh.NewNavigatorCache()
	reopened.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
	reopened.activePRPage = prPageKey(allPRsView, openPRListState, "")
	draft := gh.PR{Number: 1, State: "OPEN", IsDraft: false, HeadRefName: "feature"}
	reopened.cache = gh.NewCache("feature")
	reopened.cache.PR = &draft
	u, _ = reopened.Update(prListRefreshed{key: reopened.activePRPage, page: gh.PRPage{PRs: []gh.PR{{Number: 1, State: "OPEN", IsDraft: true, HeadRefName: "feature"}}, TotalCount: 1}})
	if got := u.(Model).cache.PR; !got.IsDraft {
		t.Fatalf("list page did not refresh the cached PR: %#v", got)
	}
}

func TestListStatusCloseUpdatesCachedBranchPR(t *testing.T) {
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, allPRsView, openPRListState
	m.currentBranch = "feature"
	m.navigator = gh.NewNavigatorCache()
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	stale := gh.PR{Number: 12, State: "OPEN", HeadRefName: "feature", Title: "x"}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &stale
	m.prPages = map[string]prPageState{m.activePRPage: {prs: []gh.PR{stale}, total: 1, loaded: true, fresh: true}}

	// Closing via the status popup on the list screen must update the
	// branch cache too, or withLocalPR keeps re-injecting the stale copy.
	closed := stale
	closed.State = "CLOSED"
	u, _ := m.Update(prStatusDone{pr: closed, target: "closed"})
	m = u.(Model)
	if m.cache.PR.State != "CLOSED" {
		t.Fatalf("cached branch PR state = %q", m.cache.PR.State)
	}
	for _, pr := range m.openPRs {
		if pr.Number == 12 {
			t.Fatalf("closed PR re-injected into the open list: %#v", m.openPRs)
		}
	}
}

func TestRefreshOnDefaultBranchDoesNotInventLocalPR(t *testing.T) {
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, allPRsView, openPRListState
	m.currentBranch, m.defaultBranch = "main", "main"
	m.navigator = gh.NewNavigatorCache()
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.cache = gh.NewCache("main")
	m.prPages = map[string]prPageState{m.activePRPage: {
		prs: []gh.PR{{Number: 2, State: "OPEN", Title: "other"}}, total: 1, loaded: true, fresh: true,
	}}

	// The refresh-triggered lookup finds no PR for main: that must not
	// mark the default branch as local work with a synthetic PR row.
	u, _ := m.Update(currentBranchPRLoaded{err: gh.ErrPRNotFound, stateOnly: true})
	m = u.(Model)
	if m.localAvailable {
		t.Fatal("default branch marked as an unpublished local PR")
	}
	for _, pr := range m.openPRs {
		if pr.State == "LOCAL" {
			t.Fatalf("synthetic Local PR row injected: %#v", m.openPRs)
		}
	}
}

func TestLocalPRAppearsInEveryOpenView(t *testing.T) {
	m := testModel()
	local := gh.PR{}
	for _, view := range []prView{assignedView, reviewRequestedView, allPRsView, authoredView, needsMeView} {
		if !m.matchesView(local, view) {
			t.Fatalf("local PR missing from %s", m.viewName(view))
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
	u, _ := m.Update(prListRefreshed{generation: 1, key: m.activePRPage, page: gh.PRPage{ViewerLogin: "me", PRs: []gh.PR{pr}, TotalCount: 1}})
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

func TestRemotePRHeaderAndExplorerShowMergeReadiness(t *testing.T) {
	m := testModel()
	m.w = 180
	m.remote = true
	m.cache.PR = &gh.PR{Number: 7, State: "OPEN"}
	m.mergeReadiness = git.MergeReadiness{Behind: 3, ConflictFiles: []string{"conflict.go"}}
	m.files = []git.ChangedFile{{Status: "M", Path: "conflict.go"}, {Status: "A", Path: "clean.go"}}
	m.explorer.Width = 80
	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(header, "3 behind") || !strings.Contains(header, "1 conflict files") {
		t.Fatalf("merge readiness header = %q", header)
	}
	explorer, _ := m.buildFileExplorer()
	plain := ansi.Strip(explorer)
	if !strings.Contains(plain, "⚠ 1 conflicts") || !strings.Contains(plain, "⚠ M conflict.go") || strings.Contains(plain, "⚠ A clean.go") {
		t.Fatalf("merge readiness explorer = %q", plain)
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
	if text, style := mergeState(gh.PR{Number: 1, MergeStateStatus: "BLOCKED"}); style.Render(text) != stRedF.Render("blocked") {
		t.Fatalf("blocked merge = %q", style.Render(text))
	}
	if text, style := mergeState(gh.PR{Number: 1, Mergeable: "MERGEABLE", MergeStateStatus: "UNSTABLE"}); style.Render(text) != stGreenF.Render("⇄ mergeable") {
		t.Fatalf("unstable merge = %q", style.Render(text))
	}
	if text, style := checkState([]gh.PRCheck{{Status: "IN_PROGRESS"}}); style.Render(text) != stAttention.Render("◐ CI 1 pending") {
		t.Fatalf("pending CI = %q", style.Render(text))
	}
	if cDoneEmphasis != "#8957e5" {
		t.Fatalf("merged palette = %s", cDoneEmphasis)
	}
	if got := prStateBadgeColor("MERGED"); got != cDoneEmphasis {
		t.Fatalf("merged badge = %s", got)
	}
}

func TestSelectedPRRowPreservesSemanticStatusColors(t *testing.T) {
	m := testModel()
	m.list.Width = 140
	pr := gh.PR{
		Number:           12,
		Title:            "status colors",
		State:            "OPEN",
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "CLEAN",
		Checks:           []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}},
		PreviewLoaded:    true,
		Additions:        8,
		Deletions:        3,
	}
	rows := strings.Join(m.renderPRRow(pr, true, ""), "\n")
	bg := lipgloss.Color(cSelectedBg)
	for _, want := range []string{
		stGreenF.Background(bg).Render("⇄ mergeable"),
		stGreenF.Background(bg).Render("✓ CI 1 passed"),
		stGreenF.Background(bg).Render("+8"),
		stRedF.Background(bg).Render("-3"),
	} {
		if !strings.Contains(rows, want) {
			t.Fatalf("selected PR row lost semantic style %q: %q", want, rows)
		}
	}
}

func TestPRRowCacheReusesUnselectedRowsAndInvalidatesWithFilters(t *testing.T) {
	m := testModel()
	m.list.Width = 120
	m.openPRs = []gh.PR{{Number: 1, Title: "one"}, {Number: 2, Title: "two"}, {Number: 3, Title: "three"}}
	m.prStacks = buildPRStacks(m.openPRs)
	_, _ = m.buildPRListRows()
	if len(m.prRowCache) != 2 {
		t.Fatalf("initial cached rows = %d, want 2 unselected rows", len(m.prRowCache))
	}
	_, _ = m.buildPRListRows()
	if len(m.prRowCache) != 2 {
		t.Fatalf("stable render grew row cache to %d", len(m.prRowCache))
	}
	m.prCursor = 1
	_, _ = m.buildPRListRows()
	if len(m.prRowCache) != 3 {
		t.Fatalf("selection change cached rows = %d, want 3", len(m.prRowCache))
	}
	// Rows key on their full render inputs, so a data refresh keeps them;
	// eviction only happens past maxPRRowCacheEntries.
	m.applyPRFilters(0)
	if len(m.prRowCache) != 3 {
		t.Fatalf("data refresh dropped still-valid rows: %d", len(m.prRowCache))
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
	for _, want := range []string{"description", "comment", "Summary", "Preview body", "@alice", "Looks good", "mergeable", "CI 1 passed", "18 files", "+1123", "-128", "5 commits", "1 comments", "author ● @bob", "assigned ● @carol", "feature", "╭", "╰"} {
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
		if got, _ := checkState([]gh.PRCheck{{Status: "COMPLETED", Conclusion: conclusion}}); !strings.Contains(got, "failed") {
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
			t.Fatalf("view %s = %v, want %v", m.viewName(view), got, numbers)
		}
	}
}

func TestPRViewSearchAndLocalOnlyFilters(t *testing.T) {
	// GitHub's issue search cannot evaluate OR groups — it matches them as
	// free text and returns nothing — so they stay out of the server query
	// and are applied locally instead.
	query := testModel().prViewSearch(needsMeView, openPRListState, "label:bug ci:failed merge:conflicting")
	if query != "is:open label:bug" {
		t.Fatalf("needs-me query = %q", query)
	}
	server, local := splitPRFilter("is:closed author:me ci:failed merge:conflicting")
	if server != "author:me" || local != "ci:failed merge:conflicting" {
		t.Fatalf("split filter = server:%q local:%q", server, local)
	}
	server, local = splitPRFilter("(review-requested:@me OR assignee:@me OR author:@me) label:bug")
	if server != "label:bug" || local != "(review-requested:@me OR assignee:@me OR author:@me)" {
		t.Fatalf("group split = server:%q local:%q", server, local)
	}
	// An unclosed group still lands on the local side rather than leaking.
	if server, local := splitPRFilter("(a OR b"); server != "" || local != "(a OR b" {
		t.Fatalf("unclosed group = server:%q local:%q", server, local)
	}
}

// A view whose query GitHub cannot evaluate must still list and count the
// right pull requests: the server returns a superset and the tab narrows it.
func TestOrGroupViewFiltersAndCountsLocally(t *testing.T) {
	m := testModel()
	m.screen, m.viewerLogin = prListScreen, "me"
	m.views = []config.View{{Name: "Needs me", Query: "(review-requested:@me OR assignee:@me OR author:@me)"}}
	m.prView, m.prListState = 0, openPRListState
	m.activePRPage = prPageKey(0, openPRListState, "")
	m.navigator = gh.NewNavigatorCache()

	// What reaches GitHub carries no group, so the search is answerable.
	if got := m.prViewSearch(0, openPRListState, ""); got != "is:open" {
		t.Fatalf("server query = %q", got)
	}

	mine := gh.PR{Number: 1, State: "OPEN", Author: gh.PRUser{Login: "me"}}
	assigned := gh.PR{Number: 2, State: "OPEN", Assignees: []gh.PRUser{{Login: "me"}}}
	reviewing := gh.PR{Number: 3, State: "OPEN", ViewerReviewRequested: true}
	other := gh.PR{Number: 4, State: "OPEN", Author: gh.PRUser{Login: "you"}}
	superset := []gh.PR{mine, assigned, reviewing, other}
	m.prPages = map[string]prPageState{m.activePRPage: {prs: superset, total: len(superset), loaded: true, fresh: true}}
	m.navigator.PRs = superset

	m.applyPRFilters(0)
	var listed []int
	for _, pr := range m.openPRs {
		listed = append(listed, pr.Number)
	}
	if !reflect.DeepEqual(listed, []int{1, 2, 3}) {
		t.Fatalf("listed = %v, want the three that involve me", listed)
	}
	// The server total counts the superset, so the tab counts loaded rows.
	if got := m.viewCount(0); got != 3 {
		t.Fatalf("view count = %d, want 3", got)
	}
}

func TestBackToListPicksTheOriginOrFirstMatchingView(t *testing.T) {
	newModel := func() Model {
		m := testModel()
		m.viewerLogin = "me"
		m.currentBranch = "feature"
		m.navigator = gh.NewNavigatorCache()
		m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
		m.views = []config.View{
			{Name: "Assigned", Query: "assignee:@me"},
			{Name: "Authored", Query: "author:@me"},
			{Name: "All", Query: ""},
		}
		return m
	}
	authored := gh.PR{Number: 7, State: "OPEN", Author: gh.PRUser{Login: "me"}, HeadRefName: "feature"}

	// Entered from a tab: b returns to that tab even when others match.
	m := newModel()
	m.screen, m.prView = prListScreen, 2
	m.openPRs = []gh.PR{authored}
	m.prStacks = buildPRStacks(m.openPRs)
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if !m.detailOriginSet || m.detailOrigin != 2 {
		t.Fatalf("origin not recorded: set=%v view=%d", m.detailOriginSet, m.detailOrigin)
	}
	m.screen, m.cache.PR = detailScreen, &authored
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	back := u.(Model)
	if back.screen != prListScreen || back.prView != 2 {
		t.Fatalf("did not return to the origin tab: screen=%v view=%d", back.screen, back.prView)
	}
	if back.detailOriginSet {
		t.Fatal("origin outlived the return")
	}

	// Opened at startup: the first tab containing the PR wins (Assigned does
	// not match, Authored does), and the PR keeps the selection.
	startup := newModel()
	startup.screen, startup.prView = detailScreen, 0
	startup.cache.PR = &authored
	startup.navigator.PRs = []gh.PR{authored}
	u, _ = startup.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	landed := u.(Model)
	if landed.prView != 1 {
		t.Fatalf("landed on view %d (%q), want Authored", landed.prView, landed.viewName(landed.prView))
	}
	if landed.selectedPRNumber() != 7 {
		t.Fatalf("selection = #%d, want the PR just left", landed.selectedPRNumber())
	}

	// No tab contains it: fall back to the first.
	orphan := newModel()
	orphan.views = orphan.views[:2] // Assigned, Authored
	orphan.screen, orphan.prView = detailScreen, 1
	someoneElse := gh.PR{Number: 9, State: "OPEN", Author: gh.PRUser{Login: "you"}}
	orphan.cache.PR = &someoneElse
	u, _ = orphan.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if got := u.(Model).prView; got != 0 {
		t.Fatalf("fallback landed on view %d, want the first", got)
	}
}

func TestPRPaginationAppendsOnceAndCachesView(t *testing.T) {
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, allPRsView, openPRListState
	m.prListGeneration = 3
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.prPages = map[string]prPageState{m.activePRPage: {prs: []gh.PR{{Number: 1, Title: "old"}}, total: 3, endCursor: "C1", hasNext: true, loaded: true, fresh: true}}
	m.navigatorPath = filepath.Join(t.TempDir(), "navigator.json")
	m.applyPRFilters(1)
	if cmd := m.requestPRPage(false); cmd == nil || !m.prPages[m.activePRPage].loading {
		t.Fatal("next page was not scheduled")
	}
	if cmd := m.requestPRPage(false); cmd != nil {
		t.Fatal("duplicate in-flight page was scheduled")
	}
	u, _ := m.Update(prListRefreshed{
		generation: 3,
		key:        m.activePRPage,
		appendPage: true,
		page: gh.PRPage{
			PRs:        []gh.PR{{Number: 1, Title: "updated"}, {Number: 2, Title: "new"}},
			TotalCount: 2,
			PageInfo:   gh.PageInfo{EndCursor: "C2"},
		},
	})
	m = u.(Model)
	page := m.prPages[m.activePRPage]
	if page.loading || page.hasNext || len(page.prs) != 2 || page.prs[0].Title != "updated" || page.prs[1].Number != 2 {
		t.Fatalf("appended page = %#v", page)
	}
	cached, meta, ok := m.navigator.View("All")
	if !ok || meta.TotalCount != 2 || len(cached) != 2 {
		t.Fatalf("cached page = %#v meta=%#v ok=%v", cached, meta, ok)
	}
}

func TestPRViewSwitchUsesFreshPageWithoutRequest(t *testing.T) {
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, assignedView, openPRListState
	m.activePRPage = prPageKey(assignedView, openPRListState, "")
	reviewKey := prPageKey(reviewRequestedView, openPRListState, "")
	m.prPages = map[string]prPageState{
		m.activePRPage: {prs: []gh.PR{{Number: 1}}, total: 1, loaded: true, fresh: true},
		reviewKey:      {prs: []gh.PR{{Number: 2}}, total: 1, loaded: true, fresh: true},
	}
	m.prView = reviewRequestedView
	_ = m.applyPRViewState(0)
	if m.activePRPage != reviewKey || m.listRefreshing || m.prPages[reviewKey].loading || len(m.openPRs) != 1 || m.openPRs[0].Number != 2 {
		t.Fatalf("fresh view switch = key:%q refreshing:%v page=%#v prs=%#v", m.activePRPage, m.listRefreshing, m.prPages[reviewKey], m.openPRs)
	}
}

func TestPRPaginationStartsWhenNavigationReachesLastRow(t *testing.T) {
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, allPRsView, openPRListState
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.prPages = map[string]prPageState{m.activePRPage: {prs: []gh.PR{{Number: 1}, {Number: 2}}, total: 3, endCursor: "C1", hasNext: true, loaded: true, fresh: true}}
	m.applyPRFilters(1)
	if cmd := m.moveCursorTo(1); cmd == nil || !m.prPages[m.activePRPage].loading {
		t.Fatal("last-row navigation did not request the next page")
	}
}

func TestLocalOnlyFilterContinuesUntilVisiblePage(t *testing.T) {
	m := testModel()
	m.screen, m.prView, m.prListState = prListScreen, allPRsView, openPRListState
	m.filterQuery = "ci:failed"
	m.activePRPage = prPageKey(allPRsView, openPRListState, m.filterQuery)
	m.prPages = map[string]prPageState{m.activePRPage: {loading: true}}
	m.navigatorPath = filepath.Join(t.TempDir(), "navigator.json")
	u, cmd := m.Update(prListRefreshed{
		key: m.activePRPage,
		page: gh.PRPage{
			PRs:        []gh.PR{{Number: 1, CheckRollupState: "SUCCESS"}},
			TotalCount: 2,
			PageInfo:   gh.PageInfo{HasNextPage: true, EndCursor: "C1"},
		},
	})
	m = u.(Model)
	if cmd == nil || len(m.openPRs) != 0 || !m.prPages[m.activePRPage].loading {
		t.Fatalf("local filter did not continue: visible=%#v page=%#v", m.openPRs, m.prPages[m.activePRPage])
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
	u, _ = m.Update(prListRefreshed{generation: m.prListGeneration, key: m.activePRPage, page: gh.PRPage{ViewerLogin: "me", PRs: []gh.PR{{Number: 2, State: "CLOSED", Title: "closed"}}, TotalCount: 1}})
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
	if m.prView != assignedView || !m.listRefreshing || len(m.openPRs) != 1 || m.openPRs[0].Number != 1 {
		t.Fatalf("cache-first assigned view = view:%v refreshing:%v prs:%#v", m.prView, m.listRefreshing, m.openPRs)
	}
}

func TestClosedPRsDoNotRenderAsStacks(t *testing.T) {
	m := testModel()
	m.prListState = closedPRListState
	m.filteredPRs = []gh.PR{
		{Number: 1, State: "CLOSED", HeadRefName: "one", BaseRefName: "main"},
		{Number: 2, State: "CLOSED", HeadRefName: "two", BaseRefName: "one"},
	}
	m.applyPRFilters(0)
	for _, stack := range m.prStacks {
		if len(stack.entries) != 1 {
			t.Fatalf("closed stack = %#v", stack)
		}
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
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if m.prView != allPRsView || m.prListState != closedPRListState || !m.listRefreshing || len(m.openPRs) != 0 {
		t.Fatalf("closed search = view:%v state:%v refreshing:%v prs:%#v", m.prView, m.prListState, m.listRefreshing, m.openPRs)
	}
	u, _ = m.Update(prListRefreshed{generation: m.prListGeneration, key: m.activePRPage, page: gh.PRPage{PRs: []gh.PR{{Number: 2, State: "CLOSED", Title: "closed"}}, TotalCount: 1}})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if m.filterQuery != "" || m.prListState != openPRListState || !m.listRefreshing || len(m.openPRs) != 1 || m.openPRs[0].Number != 1 {
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

func TestPRListHeaderShowsRepositoryAtWideAndNarrowWidths(t *testing.T) {
	for _, width := range []int{50, 120} {
		m := testModel()
		m.repository, m.w = "acme/project", width
		if out := ansi.Strip(m.renderPRListHeader()); !strings.Contains(out, "acme/project") {
			t.Fatalf("width %d header missing repository: %q", width, out)
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
	if !m.filterEditing || m.filterQuery != "label:bug" || len(m.openPRs) != 2 {
		t.Fatalf("editing unexpectedly fetched/filtered: editing:%v query:%q prs:%#v", m.filterEditing, m.filterQuery, m.openPRs)
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
	for _, want := range []string{"[ Assigned ? ]", "[ Review requested 1 ]", "[ All 2 ]", "[ Authored ? ]", "[ Needs me 1 ]", "[ Closed ? ]"} {
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
	for _, want := range []string{"#1 · 3 PRs", "├ #1", "├ #2", "└ #3"} {
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
	if !strings.Contains(ansi.Strip(m.buildPRList()), "▸ #1") {
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
	u, _ = m.Update(prListRefreshed{page: gh.PRPage{PRs: []gh.PR{{Number: 2}, {Number: 3}}, TotalCount: 2}})
	m = u.(Model)
	if len(m.openPRs) != 2 || m.openPRs[m.prCursor].Number != 2 {
		t.Fatalf("successful refresh lost selection: prs=%v cursor=%d", m.openPRs, m.prCursor)
	}
	// Persisting rides an async Cmd; run it here to check the payload.
	if msg := saveNavigatorCacheCmd(m.navigatorPath, m.navigator)(); msg != nil {
		t.Fatalf("navigator save failed: %#v", msg)
	}
	cached, err := gh.LoadNavigatorCache(m.navigatorPath)
	if err != nil || len(cached.PRs) != 2 {
		t.Fatalf("navigator cache not saved: %#v err=%v", cached, err)
	}
}

func TestPRListRefreshFailureNamesSetupProblems(t *testing.T) {
	m := testModel()
	authErr := errors.New("gh pr list: exit status 4: To get started with GitHub CLI, please run:  gh auth login")
	u, _ := m.Update(prListRefreshed{err: authErr})
	m = u.(Model)
	if strings.Contains(m.githubStatus, "Offline") || !strings.Contains(m.githubStatus, "gh auth login") {
		t.Fatalf("auth failure reported as offline: %q", m.githubStatus)
	}
	if !strings.Contains(m.githubStatus, "showing cached PR list") {
		t.Fatalf("cached-list note lost: %q", m.githubStatus)
	}
}

func TestStalePRListRefreshCannotRestoreMergedPR(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.prListGeneration = 2
	m.listRefreshing = true
	m.activePRPage = "new"
	m.prPages = map[string]prPageState{"old": {loading: true}}
	m.openPRs = []gh.PR{{Number: 2}}
	u, cmd := m.Update(prListRefreshed{generation: 1, key: "old", page: gh.PRPage{PRs: []gh.PR{{Number: 1}}, TotalCount: 1}})
	m = u.(Model)
	if cmd != nil || len(m.openPRs) != 1 || m.openPRs[0].Number != 2 || !m.listRefreshing || m.prPages["old"].loading {
		t.Fatalf("stale PR list applied: prs=%v refreshing=%v old=%#v cmd=%v", m.openPRs, m.listRefreshing, m.prPages["old"], cmd)
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
	u, _ = next.Update(prListRefreshed{generation: 5, page: gh.PRPage{PRs: []gh.PR{{Number: 1}}, TotalCount: 1}})
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
	m.prStacks = buildPRStacks(m.openPRs)
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
	pr := gh.PR{Number: 14, URL: "https://example/pr/14", Title: "remote", HeadRefName: "feature", BaseRefName: "main", BaseRefOID: "historical-base"}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &pr
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, cmd := m.Update(remoteLoaded{pr: pr, headRef: "HEAD", base: "main", diffBase: "historical-base", commits: []git.Commit{{SHA: "abc1234"}}, files: []git.ChangedFile{{Status: "M", Path: "a.go"}}, comments: []gh.Comment{{ID: 1, Body: "cached"}}})
	m = u.(Model)
	defer m.close()
	if cmd == nil || m.diffTerminal == nil || m.refreshing || len(m.cache.Comments) != 1 {
		t.Fatalf("remote load incomplete: terminal=%v refreshing=%v cache=%#v", m.diffTerminal, m.refreshing, m.cache)
	}
	if m.diffBase != "historical-base" || m.reviewRange != "historical-base...HEAD" {
		t.Fatalf("remote review range = base:%q range:%q", m.diffBase, m.reviewRange)
	}
	// The handler applies the ranges gathered by fetchRemotePR instead of
	// spawning git itself.
	if len(m.commits) != 1 || len(m.files) != 1 || m.files[0].Path != "a.go" {
		t.Fatalf("remote ranges not applied: commits=%d files=%#v", len(m.commits), m.files)
	}
	// The snapshot lands in memory synchronously; the disk write rides an
	// async Cmd so the Update goroutine never blocks on MarshalIndent + IO.
	if snapshot, ok := m.navigator.Snapshot(14); !ok || len(snapshot.Comments) != 1 {
		t.Fatalf("in-memory snapshot = %#v ok=%v", snapshot, ok)
	}
	if msg := saveNavigatorCacheCmd(m.navigatorPath, m.navigator)(); msg != nil {
		t.Fatalf("navigator save failed: %#v", msg)
	}
	cached, err := gh.LoadNavigatorCache(m.navigatorPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, ok := cached.Snapshot(14); !ok || len(snapshot.Comments) != 1 {
		t.Fatalf("remote snapshot = %#v ok=%v", snapshot, ok)
	}
}

func TestPRStatusPopupOpensFromListAndDetail(t *testing.T) {
	for _, screen := range []screen{prListScreen, detailScreen} {
		m := testModel()
		m.screen = screen
		pr := gh.PR{Number: 12, State: "OPEN", Title: "status"}
		m.cache.PR = &pr
		m.openPRs = []gh.PR{pr}
		u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
		m = u.(Model)
		popup := ansi.Strip(m.renderPRStatusPopup())
		if m.statusPR.Number != 12 || strings.Contains(popup, "\n   Open\n") || !strings.Contains(popup, "Close") || strings.Contains(popup, "Closed") || !strings.Contains(popup, "Draft") {
			t.Fatalf("screen %v status popup = %q", screen, popup)
		}
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

func TestDetailBRestoresRemotePRSelection(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.cache.PR = &gh.PR{Number: 22, State: "OPEN", Title: "selected"}
	m.prView, m.prListState = allPRsView, openPRListState
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.prPages = map[string]prPageState{m.activePRPage: {prs: []gh.PR{{Number: 11}, {Number: 22}}, loaded: true, fresh: true}}
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = u.(Model)
	if m.screen != prListScreen || m.selectedPRNumber() != 22 {
		t.Fatalf("restored selection = screen:%v PR:%d", m.screen, m.selectedPRNumber())
	}
}

func TestPRTitleAndCurrentCheckoutMarker(t *testing.T) {
	m := testModel()
	m.w, m.list.Width = 160, 120
	m.currentBranch = "feature"
	pr := gh.PR{Number: 12, State: "OPEN", Title: "Human title", HeadRefName: "feature", BaseRefName: "main"}
	m.cache.PR = &pr
	m.title = "feature"
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

func TestConflictAndCheckViewsUseLeftPane(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.mergeReadiness = git.MergeReadiness{Behind: 3, ConflictFiles: []string{"conflict.go", "nested/other.go"}}
	m.cache.PR = &gh.PR{Checks: []gh.PRCheck{
		{Name: "unit", WorkflowName: "CI", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Context: "lint", Status: "IN_PROGRESS"},
		{Name: "deploy", State: "FAILURE"},
	}}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = updated.(Model)
	plain := ansi.Strip(m.View())
	if m.active != conflictsTab || !strings.Contains(plain, "Conflicts · 2") || !strings.Contains(plain, "⚠ conflict.go") {
		t.Fatalf("conflict view = active:%v view:%q", m.active, plain)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = updated.(Model)
	plain = ansi.Strip(m.View())
	for _, want := range []string{"Checks · 3", "out of date · 3 commits behind base", "✓ unit · CI · success", "◐ lint · in progress", "✗ deploy · failure"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("check view missing %q: %q", want, plain)
		}
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.active != conversationTab {
		t.Fatalf("Esc active = %v, want Conversation", m.active)
	}
}

func TestGitHubConflictingKeepsConflictView(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.cache.PR = &gh.PR{Number: 8, Mergeable: "CONFLICTING"}
	m.mergeReadiness, m.mergeReadinessErr = applyGitHubConflictFallback(git.MergeReadiness{}, nil, *m.cache.PR)
	out, _ := m.buildConflicts()
	if !strings.Contains(ansi.Strip(out), "GitHub reports conflicts") {
		t.Fatalf("conflicting PR hid conflicts: %q", ansi.Strip(out))
	}
	dirty := gh.PR{Number: 9, MergeStateStatus: "DIRTY"}
	readiness, err := applyGitHubConflictFallback(git.MergeReadiness{}, nil, dirty)
	if err != nil || len(readiness.ConflictFiles) != 0 {
		t.Fatalf("DIRTY status should not invent conflicts: %#v err=%v", readiness, err)
	}
}

func TestCommitPickerShowsCommitSpecificCI(t *testing.T) {
	m := testModel()
	m.commits = []git.Commit{{SHA: "abc12341", Subject: "first"}, {SHA: "abc12342", Subject: "second"}}
	m.cache.PR = &gh.PR{Commits: []gh.PRCommit{
		{OID: "abc1234100000000000000000000000000000000", CheckRollupState: "SUCCESS"},
		{OID: "abc1234200000000000000000000000000000000", CheckRollupState: "FAILURE"},
	}}
	out, _ := m.buildCommits()
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "✓ abc12341 first") || !strings.Contains(plain, "✗ abc12342 second") {
		t.Fatalf("commit CI statuses missing or collided: %q", plain)
	}
	m.remote = true
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	if m.active != commitsTab || !strings.Contains(ansi.Strip(m.View()), "Commits · 2") {
		t.Fatalf("remote c did not open commit list: active=%v view=%q", m.active, ansi.Strip(m.View()))
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

func TestPRRowShowsLabelPills(t *testing.T) {
	m := testModel()
	m.list.Width = 160
	pr := gh.PR{Number: 5, State: "OPEN", Title: "Labelled", BaseRefName: "main", HeadRefName: "labels",
		Labels: []gh.PRLabel{{Name: "bug", Color: "d73a4a"}, {Name: "docs", Color: "fef2c0"}, {Name: "ui", Color: "238636"}, {Name: "infra", Color: "0e8a16"}}}
	row := ansi.Strip(strings.Join(m.renderPRRow(pr, false, ""), "\n"))
	for _, want := range []string{" bug ", " docs ", " ui ", "+1"} {
		if !strings.Contains(row, want) {
			t.Fatalf("label pill %q missing from row: %q", want, row)
		}
	}
	if strings.Contains(row, "infra") {
		t.Fatalf("fourth label should collapse into +N: %q", row)
	}
	// A PR without labels keeps its meta line untouched: branch info stays last.
	bare := gh.PR{Number: 6, State: "OPEN", Title: "Bare", BaseRefName: "main", HeadRefName: "bare"}
	bareRows := m.renderPRRow(bare, false, "")
	if meta := strings.TrimRight(ansi.Strip(bareRows[1]), " "); !strings.HasSuffix(meta, "main ← bare") {
		t.Fatalf("unlabelled meta line changed: %q", meta)
	}
	// The synthetic local PR row (Number == 0) never shows pills.
	local := gh.PR{Number: 0, Title: "Local", BaseRefName: "main", HeadRefName: "wip",
		Labels: []gh.PRLabel{{Name: "bug", Color: "d73a4a"}}}
	if localRow := ansi.Strip(strings.Join(m.renderPRRow(local, false, ""), "\n")); strings.Contains(localRow, "bug") {
		t.Fatalf("local PR row should not render pills: %q", localRow)
	}
}

func TestUserIconsAppearAcrossPRSurfaces(t *testing.T) {
	m := testModel()
	m.w, m.list.Width, m.detail.Width = 160, 70, 80
	pr := gh.PR{Number: 7, State: "OPEN", Title: "icons", BaseRefName: "main", HeadRefName: "icons", Author: gh.PRUser{Login: "alice"}, Assignees: []gh.PRUser{{Login: "bob"}}, PreviewLoaded: true}
	row := ansi.Strip(strings.Join(m.renderPRRow(pr, false, ""), "\n"))
	preview := ansi.Strip(func() string { m.openPRs = []gh.PR{pr}; return m.buildPRPreview() }())
	m.cache.PR = &pr
	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(row, "● @alice") {
		t.Fatalf("row user icon missing: %q", row)
	}
	if !strings.Contains(preview, "author ● @alice") || !strings.Contains(preview, "assigned ● @bob") {
		t.Fatalf("preview user icons missing: %q", preview)
	}
	if !strings.Contains(header, "assigned ● @bob") {
		t.Fatalf("header user icon missing: %q", header)
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

func TestEmptyLocalPRStaysOpenAfterGitHubLookup(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	st := store.ForBranch(root, "feature")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.screen = detailScreen
	m.root, m.currentBranch, m.defaultBranch = root, "feature", "main"
	m.timelinePath, m.cachePath = st.Timeline(), st.GitHubCache()
	m.files = nil

	u, _ := m.Update(githubRefreshed{err: gh.ErrPRNotFound})
	m = u.(Model)
	if m.screen != detailScreen || !m.localAvailable {
		t.Fatalf("empty explicitly opened Local PR was closed: screen=%v local=%v", m.screen, m.localAvailable)
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

func TestReloadLocalConversationReadsExternalCLIChanges(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	st := store.ForBranch(root, "feature")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Conclusion(), []byte("# Final title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := event.Create(st.Timeline(), event.Event{TS: "2026-08-11T10:00", Kind: event.Decision, Title: "external", Author: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.root, m.currentBranch, m.timelinePath = root, "feature", st.Timeline()
	m.events = nil
	m.reloadLocalConversation()
	if len(m.events) != 1 || m.events[0].ID != created.ID || m.title != "Final title" {
		t.Fatalf("reloaded local state = title %q events %+v", m.title, m.events)
	}
	items := m.conversationItems()
	if len(items) != 2 || items[0].summary == nil || items[1].event == nil {
		t.Fatalf("local summary must lead conversation: %#v", items)
	}
}

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
	if m.localEditMode != addLocalComment {
		t.Fatalf("a did not open comment editor: %v", m.localEditMode)
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
	if m.localDeleteTarget == "" {
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
	if m.localEditMode != editLocalSummary {
		t.Fatalf("e did not open summary editor: %v", m.localEditMode)
	}
	m.localEditor.SetValue("# Final outcome\n\n## Summary\n\nImplemented result.")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)
	body, err := os.ReadFile(st.Conclusion())
	if err != nil || !strings.Contains(string(body), "Implemented result") || m.title != "Final outcome" {
		t.Fatalf("TUI summary = %q title=%q err=%v", body, m.title, err)
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

func TestPendingCIPollsUntilTerminalState(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.targetGeneration = 4
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "head", PreviewLoaded: true, Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}}

	u, cmd := m.Update(ciPollTick{generation: 4, number: 12})
	m = u.(Model)
	if cmd == nil {
		t.Fatal("pending CI tick did not request fresh checks")
	}
	u, cmd = m.Update(ciPolled{generation: 4, pr: gh.PR{Number: 12, HeadRefOID: "head", PreviewLoaded: true, Checks: []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}}})
	m = u.(Model)
	if cmd != nil || prCIHealth(*m.cache.PR) != "passed" || m.githubStatus != "GitHub: CI updated now" {
		t.Fatalf("completed CI = health:%s status:%q cmd:%v", prCIHealth(*m.cache.PR), m.githubStatus, cmd)
	}
	if _, stale := m.Update(ciPollTick{generation: 3, number: 12}); stale != nil {
		t.Fatal("stale CI tick started a request")
	}
}

func TestCIPollDoesNotOverlapManualRefresh(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.targetGeneration = 2
	m.refreshing = true
	m.cache.PR = &gh.PR{Number: 12, PreviewLoaded: true, Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}}
	if _, cmd := m.Update(ciPollTick{generation: 2, number: 12}); cmd != nil {
		t.Fatal("CI poll overlapped a full refresh")
	}
}

func TestCIPollUpdatesHeadCommitActivity(t *testing.T) {
	m := testModel()
	m.screen, m.targetGeneration = detailScreen, 2
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "head", PreviewLoaded: true, Commits: []gh.PRCommit{{OID: "head", CheckRollupState: "PENDING"}}}
	m.conversationDirty = false
	u, _ := m.Update(ciPolled{generation: 2, pr: gh.PR{Number: 12, HeadRefOID: "head", Checks: []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}}})
	m = u.(Model)
	if m.cache.PR.Commits[0].CheckRollupState != "SUCCESS" || !m.conversationDirty {
		t.Fatalf("commit CI = %#v dirty=%v", m.cache.PR.Commits, m.conversationDirty)
	}
}

func TestCIPollStopsWhenHeadChanges(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.targetGeneration = 2
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "old", PreviewLoaded: true, Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}}
	u, cmd := m.Update(ciPolled{generation: 2, pr: gh.PR{Number: 12, HeadRefOID: "new", Checks: []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}}})
	m = u.(Model)
	if cmd != nil || !strings.Contains(m.githubStatus, "head changed") || prCIHealth(*m.cache.PR) != "pending" {
		t.Fatalf("changed head = health:%s status:%q cmd:%v", prCIHealth(*m.cache.PR), m.githubStatus, cmd)
	}
}

func TestCIPollUnchangedResultSkipsCacheInvalidation(t *testing.T) {
	m := testModel()
	m.screen, m.targetGeneration = detailScreen, 2
	m.cache.PR = &gh.PR{Number: 12, State: "OPEN", HeadRefOID: "head", PreviewLoaded: true,
		Checks:           []gh.PRCheck{{Name: "test", Status: "IN_PROGRESS"}},
		CheckRollupState: "PENDING",
		Commits:          []gh.PRCommit{{OID: "head", CheckRollupState: "PENDING"}}}
	m.conversationDirty = false
	key := prRowCacheKey{number: 12}
	m.prRowCache = map[prRowCacheKey][]string{key: {"row"}}
	u, cmd := m.Update(ciPolled{generation: 2, pr: gh.PR{Number: 12, State: "OPEN", HeadRefOID: "head",
		Checks: []gh.PRCheck{{Name: "test", Status: "IN_PROGRESS"}}}})
	m = u.(Model)
	if m.conversationDirty {
		t.Fatal("unchanged CI result rebuilt the conversation cards")
	}
	if _, ok := m.prRowCache[key]; !ok {
		t.Fatal("unchanged CI result dropped the PR row cache")
	}
	// A pending PR still needs the next tick even when nothing changed.
	if cmd == nil {
		t.Fatal("unchanged CI result stopped the poll chain")
	}
}

func TestCIPollReplacesCommitsWholesaleForAsyncSave(t *testing.T) {
	m := testModel()
	m.screen, m.targetGeneration = detailScreen, 2
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "head", PreviewLoaded: true,
		Commits: []gh.PRCommit{{OID: "head", CheckRollupState: "PENDING"}}}
	// saveCacheCmd copies the PR struct but shares slice backing arrays with
	// the async marshal, so the poll must swap in a fresh Commits slice
	// instead of writing elements in place.
	shared := m.cache.PR.Commits
	u, _ := m.Update(ciPolled{generation: 2, pr: gh.PR{Number: 12, HeadRefOID: "head",
		Checks: []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}}})
	m = u.(Model)
	if shared[0].CheckRollupState != "PENDING" {
		t.Fatal("CI poll wrote the shared commits slice in place")
	}
	if m.cache.PR.Commits[0].CheckRollupState != "SUCCESS" {
		t.Fatalf("cloned commits missed the rollup: %#v", m.cache.PR.Commits)
	}
}

func TestReviewRefFailureKeepsCIPollAlive(t *testing.T) {
	// countBatch runs the batch wrapper only, never the inner cmds.
	countBatch := func(cmd tea.Cmd) int {
		if cmd == nil {
			return 0
		}
		if batch, ok := cmd().(tea.BatchMsg); ok {
			return len(batch)
		}
		return 1
	}
	load := func(pr gh.PR) int {
		m := testModel()
		m.screen = detailScreen
		m.navigator = gh.NewNavigatorCache()
		m.cache = gh.NewCache("feature")
		m.cache.PR = &pr
		_, cmd := m.Update(remoteLoaded{generation: m.targetGeneration, pr: pr, refErr: errors.New("ref unavailable")})
		return countBatch(cmd)
	}
	pending := gh.PR{Number: 12, State: "OPEN", HeadRefOID: "head", PreviewLoaded: true,
		Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}}
	done := pending
	done.Checks = []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}
	// The refresh orphaned the previous poll chain, so the refErr return is
	// the only place a new one can start: a pending PR must batch exactly one
	// cmd more (the CI poll) than one whose checks are already terminal.
	if got, want := load(pending), load(done)+1; got != want {
		t.Fatalf("refErr result did not schedule the CI poll: pending batches %d cmds, terminal %d", got, want-1)
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
	// r during a publish reports the busy state instead of starting a second
	// fetch — and instead of silently doing nothing.
	m.refreshing, m.publishing = false, true
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if got := u.(Model); got.refreshing || !got.publishing {
		t.Fatalf("refresh overlapped publish: refreshing=%v publishing=%v", got.refreshing, got.publishing)
	}
}

func TestTranslateDiffMouseUsesContentBounds(t *testing.T) {
	headerHeight := logoHeight + 1 // header rows plus the review pane's top border
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

func TestPRViewNavigationSupportsHL(t *testing.T) {
	m := testModel()
	m.screen, m.prView = prListScreen, allPRsView
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.prPages = map[string]prPageState{}
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = u.(Model)
	if m.prView != authoredView {
		t.Fatalf("l view = %v", m.prView)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	m = u.(Model)
	if m.prView != allPRsView {
		t.Fatalf("h view = %v", m.prView)
	}
}

func TestReviewFocusKeys(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	// Tab from conversation focuses the review.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("Tab should focus review")
	}
	footer := ansi.Strip(m.renderFooter())
	if !strings.Contains(footer, "Tab conversation") {
		t.Fatalf("focused footer is misleading: %q", footer)
	}
	// Shift+Tab expands the review to full width.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if !m.reviewWide || !m.focusDiff {
		t.Fatal("Shift+Tab should make the review full width")
	}
	// Shift+Tab again restores the split.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if m.reviewWide {
		t.Fatal("second Shift+Tab should restore the split")
	}
	// Tab from review returns to conversation.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = u.(Model)
	if m.focusDiff || m.reviewWide {
		t.Fatal("Tab from review should return to conversation")
	}
	// Shift+Tab from conversation expands conversation to full width.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if !m.reviewWide || m.focusDiff {
		t.Fatal("Shift+Tab from conversation should expand conversation full width")
	}
	// Shift+Tab again restores the split.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if m.reviewWide {
		t.Fatal("Shift+Tab should restore the split")
	}
	// q quits from conversation.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Fatal("q on the conversation should quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("conversation q returned %T", cmd())
	}
}

func TestStaticReviewFocusKeys(t *testing.T) {
	m := testModel()
	m.diffCommand = "cat"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("l should focus the static review fallback")
	}
	// q from the review should quit (not return to conversation — use Tab).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q from the focused review should quit")
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

func TestSyncDetailKeepsScrollWhenContentUnchanged(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	long := strings.Repeat("line\n", 100)
	detail := detailContent{key: "commit:abc", raw: long, renderable: true}
	m.syncDetail(detail)
	m.detail.SetYOffset(7)
	m.syncDetail(detail)
	if m.detail.YOffset != 7 {
		t.Fatalf("re-sync of unchanged content moved scroll to %d, want 7", m.detail.YOffset)
	}
	other := strings.Repeat("other\n", 100)
	m.syncDetail(detailContent{key: "commit:def", raw: other, renderable: true})
	if m.detail.YOffset != 0 || !strings.Contains(m.detail.View(), "other") {
		t.Fatalf("new content should reset to top: offset=%d view=%q", m.detail.YOffset, m.detail.View())
	}
}

func TestSyncDetailKeepsScrollOnRenderedDiffCacheHits(t *testing.T) {
	m := testModel()
	m.diffDisplay = "cat"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	detail := detailContent{key: "commit:abc", raw: strings.Repeat("line\n", 100), renderable: true}
	cmd := m.syncDetail(detail)
	if cmd == nil {
		t.Fatal("first sync should dispatch the configured diff command")
	}
	u, _ = m.Update(cmd())
	m = u.(Model)
	m.detail.SetYOffset(7)
	if extra := m.syncDetail(detail); extra != nil || m.detail.YOffset != 7 {
		t.Fatalf("cached rendered diff re-sync moved scroll to %d, want 7", m.detail.YOffset)
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

// TestOverloadedKeysDisambiguateByScreen locks the SetEnabled toggling that
// keeps c/l from colliding: on the PR list c=Checkout and l=NextView; on the
// detail screen c=Commits and l=FocusRight. A missed SetEnabled would surface
// here as both bindings enabled on one screen.
func TestOverloadedKeysDisambiguateByScreen(t *testing.T) {
	m := testModel()
	m.ready = true
	m.w, m.h = 120, 40
	m.list = viewport.New(80, 30)
	m.detail = viewport.New(80, 30)

	m.screen = prListScreen
	m.sync()
	if m.keys.FocusRight.Enabled() {
		t.Error("PR list: l should be NextView, not FocusRight")
	}
	if m.keys.Commits.Enabled() {
		t.Error("PR list: c should be Checkout, not Commits")
	}
	if !m.keys.NextView.Enabled() {
		t.Error("PR list: NextView (l) should be enabled")
	}

	m.screen = detailScreen
	m.active = conversationTab
	m.sync()
	if m.keys.NextView.Enabled() {
		t.Error("detail: l should be FocusRight, not NextView")
	}
	if m.keys.Checkout.Enabled() {
		t.Error("detail: c should be Commits, not Checkout")
	}
	if !m.keys.FocusRight.Enabled() {
		t.Error("detail: FocusRight (l) should be enabled")
	}
}
func TestConflictsViewShowsBehindCount(t *testing.T) {
	m := testModel()
	m.mergeReadiness = git.MergeReadiness{Behind: 3}
	// No conflicting files, but behind base: the count must still show.
	out, _ := m.buildConflicts()
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "3 commits behind base") || !strings.Contains(plain, "no conflicting files") {
		t.Fatalf("conflicts view = %q", plain)
	}
	// With conflicts, the behind header still leads.
	m.mergeReadiness = git.MergeReadiness{Behind: 1, ConflictFiles: []string{"a.go"}}
	out, _ = m.buildConflicts()
	plain = ansi.Strip(out)
	if !strings.Contains(plain, "1 commit behind base") || !strings.Contains(plain, "a.go") {
		t.Fatalf("conflicts view with files = %q", plain)
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

// A new commit changes the review range and the head revision, but GitHub only
// clears "viewed" for files whose own diff changed. Marks are therefore keyed
// by path and validated against the file's diff fingerprint.
func TestReviewedMarksSurviveCommitsThatTouchOtherFiles(t *testing.T) {
	m := testModel()
	m.files = []git.ChangedFile{
		{Status: "M", Path: "kept.go", Fingerprint: "aaa:bbb"},
		{Status: "M", Path: "edited.go", Fingerprint: "ccc:ddd"},
	}
	m.fileCursor = 0
	m.toggleFileCheck()
	m.fileCursor = 1
	m.toggleFileCheck()
	for _, file := range m.files {
		if !m.fileChecked(file) {
			t.Fatalf("%s was not checked", file.Path)
		}
	}

	// A commit lands: the range moves and edited.go's diff changes, while
	// kept.go's diff is untouched.
	m.diffBase, m.headRev = "newbase", "newhead"
	m.files = []git.ChangedFile{
		{Status: "M", Path: "kept.go", Fingerprint: "aaa:bbb"},
		{Status: "M", Path: "edited.go", Fingerprint: "ccc:eee"},
	}
	if !m.fileChecked(m.files[0]) {
		t.Error("kept.go lost its reviewed mark even though its diff is unchanged")
	}
	if m.fileChecked(m.files[1]) {
		t.Error("edited.go stayed reviewed even though its diff changed")
	}
}

// Stacked PRs share paths and often identical per-file diffs, so marks must be
// scoped per PR — switching must never show another PR's progress.
func TestReviewedMarksAreScopedPerPR(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.files = []git.ChangedFile{{Status: "M", Path: "shared.go", Fingerprint: "aaa:bbb"}}

	// PR #1: check the file; the mark is persisted to #1's file.
	m.loadReviewedMarks(1, "feature")
	m.toggleFileCheck()
	if !m.fileChecked(m.files[0]) {
		t.Fatal("mark not set for PR #1")
	}

	// Switching to stacked PR #2 with the same path+fingerprint: clean slate.
	m.loadReviewedMarks(2, "feature")
	if m.fileChecked(m.files[0]) {
		t.Fatal("PR #1's mark leaked into PR #2")
	}

	// Back to PR #1: the persisted mark is restored.
	m.loadReviewedMarks(1, "feature")
	if !m.fileChecked(m.files[0]) {
		t.Fatal("PR #1's mark was lost after switching away")
	}
}

func TestReviewedMarksPersistAcrossSessions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.files = []git.ChangedFile{{Status: "M", Path: "a.go", Fingerprint: "f1"}}
	m.loadReviewedMarks(7, "feature")
	m.toggleFileCheck()

	// A fresh model (new session) sees the same marks from disk.
	fresh := testModel()
	fresh.root = m.root
	fresh.files = m.files
	fresh.loadReviewedMarks(7, "feature")
	if !fresh.fileChecked(fresh.files[0]) {
		t.Fatal("mark did not survive a session restart")
	}
}

// Async completions must land even while a modal popup owns the keyboard;
// dropping them used to leave reviewSubmitting/refreshing stuck until restart.
func TestModalPopupsDoNotDropAsyncCompletions(t *testing.T) {
	// reviewSubmitted arrives while the status popup is open.
	m := testModel()
	m.reviewSubmitting = true
	m.statusPR = gh.PR{Number: 12, State: "OPEN"}
	u, _ := m.Update(reviewSubmitted{event: gh.ReviewApproveEvent})
	if got := u.(Model); got.reviewSubmitting {
		t.Fatal("reviewSubmitted was dropped by the status popup")
	}

	// githubRefreshed arrives while the local editor is open.
	m = testModel()
	m.refreshing = true
	m.localEditMode = addLocalComment
	u, _ = m.Update(githubRefreshed{generation: m.targetGeneration, err: gh.ErrPRNotFound})
	if got := u.(Model); got.refreshing {
		t.Fatal("githubRefreshed was dropped by the editor overlay")
	}

	// Keys still go to the modal, not the main handler.
	m = testModel()
	m.statusPR = gh.PR{Number: 12, State: "OPEN"}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if got := u.(Model); got.statusPR.Number != 0 {
		t.Fatal("q did not close the status popup")
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

func TestLocalLoadRunsOffTheUpdateGoroutine(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.screen, m.prView = prListScreen, assignedView
	m.currentBranch, m.defaultBranch = "feature/x", "main"
	m.autoOpenCurrent = true
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")

	u, cmd := m.Update(currentBranchPRLoaded{pr: gh.PR{Number: 7, State: "OPEN", HeadRefName: "feature/x"}})
	m = u.(Model)
	// The handler only dispatches: the git subprocess work happens in the Cmd.
	if m.screen != prListScreen || cmd == nil || !m.refreshing {
		t.Fatalf("local load not deferred: screen=%v cmd=%v refreshing=%v", m.screen, cmd, m.refreshing)
	}

	// Stale completions are dropped.
	stale, _ := m.Update(localLoaded{generation: m.targetGeneration - 1, st: store.ForBranch(m.root, "feature/x")})
	if stale.(Model).screen != prListScreen {
		t.Fatal("stale localLoaded applied")
	}

	pr := gh.PR{Number: 7, State: "OPEN", HeadRefName: "feature/x", Title: "Seven"}
	cache := gh.NewCache("feature/x")
	cache.PR = &pr
	done, _ := m.Update(localLoaded{
		generation: m.targetGeneration,
		st:         store.ForBranch(m.root, "feature/x"),
		data:       localData{cache: cache, base: "main", diffBase: "main", headRev: "HEAD"},
	})
	m = done.(Model)
	if m.screen != detailScreen || m.title != "Seven" {
		t.Fatalf("localLoaded not applied: screen=%v title=%q", m.screen, m.title)
	}
}

func TestRecomputeViewCountsDoesNotDoubleCountCachedViews(t *testing.T) {
	m := testModel()
	m.allPRs = []gh.PR{{Number: 1, State: "OPEN"}, {Number: 2, State: "OPEN"}}
	// A cached page holds the exact server-side total for the all view.
	m.prPages = map[string]prPageState{
		prPageKey(allPRsView, openPRListState, ""): {total: 5, loaded: true},
	}

	m.recomputeViewCounts(prPageState{}, false)

	if m.viewCounts[allPRsView] != 5 {
		t.Fatalf("all view count = %d, want cached total 5", m.viewCounts[allPRsView])
	}
	// Views without a cached page still fall back to counting allPRs.
	if m.viewCounts[closedPRsView] != 0 || !m.viewCountKnown[closedPRsView] {
		t.Fatalf("closed view = %d known=%v", m.viewCounts[closedPRsView], m.viewCountKnown[closedPRsView])
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

func TestBaseResolvedAppliesOnlyCurrentGeneration(t *testing.T) {
	m := testModel()
	m.targetGeneration = 3
	m.base, m.diffBase, m.headRev, m.reviewRange = "main", "old-base", "HEAD", "old-base"

	// Stale generation: dropped.
	u, _ := m.Update(baseResolved{generation: 2, base: "main", diffBase: "new-base", headRev: "HEAD", reviewRange: "new-base"})
	if u.(Model).diffBase != "old-base" {
		t.Fatalf("stale baseResolved applied: %q", u.(Model).diffBase)
	}

	// Unchanged range: no-op.
	u, _ = m.Update(baseResolved{generation: 3, base: "main", diffBase: "old-base", headRev: "HEAD", reviewRange: "old-base"})
	if u.(Model).fileCursor != 0 && u.(Model).diffBase != "old-base" {
		t.Fatal("unchanged range should be a no-op")
	}

	// Changed range: applied with the gathered scans.
	u, _ = m.Update(baseResolved{
		generation: 3, base: "main", diffBase: "new-base", headRev: "HEAD", reviewRange: "new-base",
		commits: []git.Commit{{SHA: "abc"}}, files: []git.ChangedFile{{Status: "M", Path: "x.go"}},
	})
	m = u.(Model)
	if m.diffBase != "new-base" || m.reviewRange != "new-base" || len(m.commits) != 1 || len(m.files) != 1 {
		t.Fatalf("baseResolved not applied: diffBase=%q commits=%d files=%d", m.diffBase, len(m.commits), len(m.files))
	}
}

func TestCacheSavedUpdatesStatus(t *testing.T) {
	m := testModel()
	u, _ := m.Update(cacheSaved{err: errors.New("disk full")})
	if got := u.(Model).status; got != "GitHub cache: disk full" {
		t.Fatalf("error status = %q", got)
	}
	m.status = "GitHub cache: disk full"
	u, _ = m.Update(cacheSaved{})
	if got := u.(Model).status; got != "" {
		t.Fatalf("stale cache error kept: %q", got)
	}
}

func TestCheckoutReloadRunsAsCommandAndKeepsModelOnFailure(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	u, cmd := m.Update(prActionDone{action: checkoutPR, number: 14, pr: gh.PR{Number: 14}})
	m = u.(Model)
	// The rebuild (New + local hydration) runs in the Cmd, not the handler.
	if cmd == nil || !m.refreshing || !strings.Contains(m.status, "reloading") {
		t.Fatalf("checkout rebuild not deferred: cmd=%v refreshing=%v status=%q", cmd, m.refreshing, m.status)
	}
	// A failed rebuild keeps the old model alive instead of a closed husk.
	u, _ = m.Update(checkoutReloaded{number: 14, err: errors.New("checkout reload: boom")})
	m = u.(Model)
	if m.refreshing || m.status != "checkout reload: boom" || m.screen != prListScreen {
		t.Fatalf("failure did not keep old model: refreshing=%v status=%q", m.refreshing, m.status)
	}
}

func TestRemoteLoadedKeepsCachedReviewsOnFetchFailure(t *testing.T) {
	m := testModel()
	m.remote, m.screen = true, detailScreen
	m.cache = gh.NewCache("feature")
	m.cache.Reviews = []gh.Review{{ID: 1}}
	m.cache.ReviewComments = []gh.ReviewThreadComment{{ID: 2}}
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")

	u, _ := m.Update(remoteLoaded{
		pr: gh.PR{Number: 5}, headRef: "HEAD",
		reviewsErr:        errors.New("api down"),
		reviewCommentsErr: errors.New("api down"),
	})
	m = u.(Model)
	if len(m.cache.Reviews) != 1 || len(m.cache.ReviewComments) != 1 {
		t.Fatalf("failed fetch wiped cached reviews: %#v", m.cache)
	}
	if !strings.Contains(m.githubStatus, "reviews") {
		t.Fatalf("stale reviews not reported: %q", m.githubStatus)
	}
}

func TestBackgroundSyncKeepsPreviewScroll(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator = gh.NewNavigatorCache()
	m.openPRs = []gh.PR{
		{Number: 1, State: "OPEN", Title: "one", Body: strings.Repeat("line\n", 80)},
		{Number: 2, State: "OPEN", Title: "two"},
	}
	m.prCursor = 0
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
	m.prCursor = 1
	m.sync()
	if m.detail.YOffset != 0 {
		t.Fatalf("selection change kept stale scroll offset %d", m.detail.YOffset)
	}
}

func TestCurrentBranchPRLoadedKeepsListStateOnDetailScreen(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.currentBranch, m.defaultBranch = "feature/x", "main"
	m.prView, m.prListState = assignedView, openPRListState
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
	u, _ := m.Update(currentBranchPRLoaded{pr: gh.PR{Number: 9, State: "MERGED", HeadRefName: "feature/x"}})
	m = u.(Model)
	if m.prView != assignedView || m.prListState != openPRListState {
		t.Fatalf("detail screen rewrote list state: view=%v state=%v", m.prView, m.prListState)
	}
}

func TestReopenKeepsDraftAndClosedFilterMatchesMerged(t *testing.T) {
	// Reopening a closed draft keeps its draftness in the optimistic update.
	pr := optimisticStatus(gh.PR{Number: 3, State: "CLOSED", IsDraft: true}, "open")
	if !pr.IsDraft || pr.State != "OPEN" {
		t.Fatalf("reopened draft = %#v", pr)
	}
	// The explicit draft -> open transition clears it.
	if pr := optimisticStatus(gh.PR{Number: 3, State: "OPEN", IsDraft: true}, "open"); pr.IsDraft {
		t.Fatalf("ready-for-review kept draft: %#v", pr)
	}

	// is:closed matches MERGED like GitHub search and matchesListState.
	if !matchesPRFilter(gh.PR{State: "MERGED"}, "is:closed", "") {
		t.Fatal("is:closed rejected a merged PR")
	}
	if matchesPRFilter(gh.PR{State: "OPEN"}, "is:closed", "") {
		t.Fatal("is:closed matched an open PR")
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
	if m.richContentCmd() != nil {
		t.Fatal("zero/negative width still dispatched a render")
	}
	m.list.Width = 87
	if m.richContentCmd() == nil {
		t.Fatal("first dispatch skipped")
	}
	// Same content and width: no re-render, no avatar re-download.
	if m.richContentCmd() != nil {
		t.Fatal("unchanged content dispatched again")
	}
	m.list.Width = 47
	if m.richContentCmd() == nil {
		t.Fatal("width change did not re-dispatch")
	}
}

func TestStaleGenerationRichContentWithMatchingKeyStillApplies(t *testing.T) {
	m := testModel()
	pr := &gh.PR{Number: 1, Body: "```mermaid\ngraph TD;A-->B;\n```"}
	pr.Author.Login = "alice"
	m.cache.PR = pr
	m.list.Width = 87
	if m.richContentCmd() == nil {
		t.Fatal("first dispatch skipped")
	}
	// A refresh mid-render bumps the generation while the content and width
	// stay the same: nothing resets lastRichContentKey, so if the in-flight
	// result were discarded, richContentCmd would return nil forever and the
	// mermaid diagrams and avatar colors would never render.
	m.targetGeneration++
	key := richContentKey(m.list.Width-7, m.cache.PR, m.cache.Comments, m.cache.Activities)
	u, _ := m.Update(richBodiesLoaded{key: key, bodies: map[string]string{pr.Body: "rendered"}})
	m = u.(Model)
	if m.richBodies[pr.Body] != "rendered" {
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
	if _, ok := m.richBodies["other"]; ok {
		t.Fatal("mismatched-key result was applied")
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
	title := next.remoteDeleteTitle
	if !utf8.ValidString(title) {
		t.Fatalf("truncation split a rune: %q", title)
	}
	if !strings.HasSuffix(title, "…") || strings.Contains(title, "�") {
		t.Fatalf("title = %q", title)
	}
}

func TestApplyPRFiltersKeepsRowCacheWithinBound(t *testing.T) {
	m := testModel()
	m.navigator = gh.NewNavigatorCache()
	m.prRowCache = map[prRowCacheKey][]string{{number: 1}: {"row"}}
	m.applyPRFilters(0)
	if len(m.prRowCache) != 1 {
		t.Fatal("data refresh dropped still-valid row renders")
	}
	for i := 0; i <= maxPRRowCacheEntries; i++ {
		m.prRowCache[prRowCacheKey{number: i}] = []string{"row"}
	}
	m.applyPRFilters(0)
	if len(m.prRowCache) != 0 {
		t.Fatalf("over-cap cache not evicted: %d", len(m.prRowCache))
	}
}

func TestRefreshAppliesFreshReadinessOnUnchangedRange(t *testing.T) {
	m := testModel()
	m.targetGeneration = 3
	m.base, m.diffBase, m.headRev, m.reviewRange = "main", "origin/main", "HEAD", "origin/main"
	m.mergeReadiness = git.MergeReadiness{Behind: 0}
	m.commits = []git.Commit{{SHA: "old"}}

	// The range string is unchanged, but the base ref moved underneath it:
	// behind count, conflicts, and the scans must still refresh.
	u, _ := m.Update(baseResolved{
		generation: 3, base: "main", diffBase: "origin/main", headRev: "HEAD", reviewRange: "origin/main",
		commits:     []git.Commit{{SHA: "new1"}, {SHA: "new2"}},
		files:       []git.ChangedFile{{Status: "M", Path: "a.go"}},
		readiness:   git.MergeReadiness{Behind: 4, ConflictFiles: []string{"a.go"}},
		readinessOK: true,
	})
	m = u.(Model)
	if m.mergeReadiness.Behind != 4 || len(m.mergeReadiness.ConflictFiles) != 1 {
		t.Fatalf("stale readiness kept: %#v", m.mergeReadiness)
	}
	if len(m.commits) != 2 || len(m.files) != 1 {
		t.Fatalf("stale scans kept: commits=%d files=%d", len(m.commits), len(m.files))
	}
	if m.diffTerminal != nil {
		t.Fatal("unchanged range must not restart the review terminal")
	}
}

// A notice must not outlive the next thing the user does, or the footer keeps
// reporting an old action while newer work runs unannounced.
func TestNoticeClearsOnTheNextKeyAndRefreshAlwaysReports(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator = gh.NewNavigatorCache()
	pr := gh.PR{Number: 12, State: "OPEN", Title: "x"}
	m.openPRs = []gh.PR{pr}
	m.prStacks = buildPRStacks(m.openPRs)

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
	m.listRefreshing = true
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
		next, _ := m.openLocalEditor(addRemoteComment, "line", "")
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
		next, _ := m.openLocalEditor(editReviewBody, strings.Repeat("x", 300), "review-submit")
		next.reviewSubmitEvent, next.reviewSubmitTyping = gh.ReviewCommentEvent, true
		budget := max(36, min(80, w-14)) - 4
		if got := next.localEditor.Width(); got > budget {
			t.Fatalf("w=%d editor width %d exceeds the popup budget %d", w, got, budget)
		}
		// The popup must not need to wrap anything the editor rendered.
		for _, line := range strings.Split(ansi.Strip(next.renderReviewSubmitPopup()), "\n") {
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
	next, _ := m.openLocalEditor(addRemoteComment, "", "")
	hint := ansi.Strip(next.renderLocalEditorPopup())
	if !strings.Contains(hint, "Ctrl+S send") {
		t.Fatalf("editor hint = %q", hint)
	}

	// Enter stays a newline; Ctrl+S sends.
	next.localEditor.SetValue("hello")
	typed, _ := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := typed.(Model); got.localEditMode != addRemoteComment || !strings.Contains(got.localEditor.Value(), "\n") {
		t.Fatalf("enter did not insert a newline: mode=%v value=%q", got.localEditMode, got.localEditor.Value())
	}
	sent, cmd := next.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if got := sent.(Model); got.localEditMode != noLocalEdit || cmd == nil {
		t.Fatalf("ctrl+s did not send: mode=%v cmd=%v", got.localEditMode, cmd)
	}
}

func TestPRFilterOrGroups(t *testing.T) {
	assigned := gh.PR{Number: 1, State: "OPEN", Assignees: []gh.PRUser{{Login: "me"}}}
	requested := gh.PR{Number: 2, State: "OPEN", ViewerReviewRequested: true}
	neither := gh.PR{Number: 3, State: "OPEN", Author: gh.PRUser{Login: "someone"}}

	const needsMe = "(assignee:@me OR review-requested:@me)"
	for _, tc := range []struct {
		pr   gh.PR
		want bool
	}{{assigned, true}, {requested, true}, {neither, false}} {
		if got := matchesPRFilter(tc.pr, needsMe, "me"); got != tc.want {
			t.Fatalf("#%d needs-me = %v, want %v", tc.pr.Number, got, tc.want)
		}
	}

	// Groups AND with the rest of the query.
	if matchesPRFilter(assigned, needsMe+" is:closed", "me") {
		t.Fatal("group ignored the trailing is:closed term")
	}
	if !matchesPRFilter(assigned, "is:open "+needsMe, "me") {
		t.Fatal("leading term broke the group")
	}
	// An unclosed group still evaluates its alternatives.
	if !matchesPRFilter(requested, "(assignee:@me OR review-requested:@me", "me") {
		t.Fatal("unclosed group rejected a matching PR")
	}
	// Plain queries keep working, free text included.
	if !matchesPRFilter(neither, "someone", "me") || matchesPRFilter(neither, "nobody", "me") {
		t.Fatal("free-text matching regressed")
	}
}

func TestConfiguredViewsDriveTabsSearchAndCounts(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.viewerLogin = "me"
	m.views = []config.View{
		{Name: "Mine", Query: "author:@me"},
		{Name: "Bugs", Query: "label:bug"},
		{Name: "Done", Query: "is:closed"},
	}
	mine := gh.PR{Number: 1, State: "OPEN", Author: gh.PRUser{Login: "me"}}
	bug := gh.PR{Number: 2, State: "OPEN", Author: gh.PRUser{Login: "you"}, Labels: []gh.PRLabel{{Name: "bug"}}}
	done := gh.PR{Number: 3, State: "MERGED", Author: gh.PRUser{Login: "you"}}
	m.navigator = gh.NewNavigatorCache()
	m.navigator.PRs = []gh.PR{mine, bug, done}
	m.allPRs = m.navigator.PRs

	// The tab's own query decides membership and its open/closed bucket.
	for _, tc := range []struct {
		view prView
		pr   gh.PR
		want bool
	}{{0, mine, true}, {0, bug, false}, {1, bug, true}, {1, mine, false}, {2, done, true}} {
		if got := m.matchesView(tc.pr, tc.view); got != tc.want {
			t.Fatalf("%s matches #%d = %v, want %v", m.viewName(tc.view), tc.pr.Number, got, tc.want)
		}
	}
	if m.standardPRListState(2) != closedPRListState || m.standardPRListState(0) != openPRListState {
		t.Fatal("view state not derived from the query")
	}

	// The search sends the tab's query alongside the state and user filter.
	if got := m.prViewSearch(1, openPRListState, "ci:failed rebase"); got != "is:open label:bug rebase" {
		t.Fatalf("search = %q", got)
	}

	// Counts and the tab bar follow the configured names and order.
	m.recomputeViewCounts(prPageState{}, false)
	if m.viewCount(0) != 1 || m.viewCount(1) != 1 || m.viewCount(2) != 1 {
		t.Fatalf("counts = %v", m.viewCounts)
	}
	m.w = 200
	header := ansi.Strip(m.renderPRListHeader())
	if !strings.Contains(header, "[ Mine 1 ]") || !strings.Contains(header, "[ Done 1 ]") {
		t.Fatalf("tab bar = %q", header)
	}
	if strings.Contains(header, "Assigned") {
		t.Fatal("tab bar still shows a built-in view")
	}

	// [ ] cycle over the configured list, not a fixed count of six.
	m.prView = 2
	if next := m.stepView(1); next != 0 {
		t.Fatalf("wrap-around landed on %d", next)
	}
	if prev := m.stepView(-1); prev != 1 {
		t.Fatalf("previous view = %d", prev)
	}
}

// The view manager edits a draft: Esc discards, Ctrl+S writes the config and
// reloads the tabs.
func TestViewManagerEditsReorderAndSave(t *testing.T) {
	global := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	m := testModel()
	m.screen, m.w, m.h = prListScreen, 120, 40
	m.root = t.TempDir()
	m.navigator = gh.NewNavigatorCache()
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")

	press := func(model Model, keys ...string) Model {
		t.Helper()
		for _, k := range keys {
			var msg tea.KeyMsg
			switch k {
			case "enter":
				msg = tea.KeyMsg{Type: tea.KeyEnter}
			case "tab":
				msg = tea.KeyMsg{Type: tea.KeyTab}
			case "esc":
				msg = tea.KeyMsg{Type: tea.KeyEsc}
			case "ctrl+s":
				msg = tea.KeyMsg{Type: tea.KeyCtrlS}
			default:
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
			}
			u, _ := model.Update(msg)
			model = u.(Model)
		}
		return model
	}

	m = press(m, "V")
	if !m.viewManager || len(m.viewDraft) != len(config.DefaultViews()) {
		t.Fatalf("manager did not open: %v draft=%d", m.viewManager, len(m.viewDraft))
	}
	if popup := ansi.Strip(m.renderViewManagerPopup()); !strings.Contains(popup, "Assigned") || !strings.Contains(popup, "assignee:@me") {
		t.Fatalf("popup = %q", popup)
	}

	// The manager opens on the current tab; move to the top first.
	if m.viewCursor != int(m.prView) {
		t.Fatalf("manager opened on view %d, want %d", m.viewCursor, m.prView)
	}
	m.viewCursor = 0

	// Reorder: J swaps the first two tabs and follows the selection.
	m = press(m, "J")
	if m.viewDraft[0].Name != "Review requested" || m.viewDraft[1].Name != "Assigned" || m.viewCursor != 1 {
		t.Fatalf("reorder = %v cursor=%d", m.viewDraft, m.viewCursor)
	}

	// Rename the selected view through the two-field form.
	m = press(m, "e")
	if m.viewEditField != viewEditName {
		t.Fatal("edit form did not focus the name")
	}
	m.viewNameInput.SetValue("Mine")
	m = press(m, "tab")
	m.viewQueryInput.SetValue("author:@me")
	m = press(m, "enter")
	if m.viewEditField != viewEditNone || m.viewDraft[1].Name != "Mine" || m.viewDraft[1].Query != "author:@me" {
		t.Fatalf("edit not applied: %#v", m.viewDraft[1])
	}

	// A duplicate name is rejected instead of silently dropping a view.
	m = press(m, "e")
	m.viewNameInput.SetValue("review requested")
	m = press(m, "enter")
	if m.viewManagerError == "" || m.viewEditField == viewEditNone {
		t.Fatalf("duplicate name accepted: err=%q", m.viewManagerError)
	}
	m = press(m, "esc")

	// New view, then delete one.
	m = press(m, "n")
	m.viewNameInput.SetValue("Bugs")
	m = press(m, "tab")
	m.viewQueryInput.SetValue("label:bug")
	m = press(m, "enter")
	if last := m.viewDraft[len(m.viewDraft)-1]; last.Name != "Bugs" || last.Query != "label:bug" {
		t.Fatalf("new view = %#v", last)
	}
	before := len(m.viewDraft)
	m = press(m, "d")
	if len(m.viewDraft) != before-1 {
		t.Fatalf("delete kept %d views", len(m.viewDraft))
	}

	// Esc discards everything.
	discarded := press(m, "esc")
	if discarded.viewManager || len(discarded.views) != len(config.DefaultViews()) || discarded.views[0].Name != "Assigned" {
		t.Fatalf("esc did not discard: views=%v", discarded.views)
	}

	// Ctrl+S persists and applies. The tab that was selected before the edit
	// ("Review requested", index 1 of the defaults) keeps its selection even
	// though reordering moved it to the front.
	m.prView = 1
	saved := press(m, "ctrl+s")
	if saved.viewManager || saved.views[0].Name != "Review requested" || saved.views[1].Name != "Mine" {
		t.Fatalf("save did not apply: %v", saved.views)
	}
	if saved.viewName(saved.prView) != "Review requested" || saved.prView != 0 {
		t.Fatalf("selection did not follow the renamed order: %q (%d)", saved.viewName(saved.prView), saved.prView)
	}
	reloaded, err := config.Load(saved.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Views) != len(saved.views) || reloaded.Views[1].Name != "Mine" {
		t.Fatalf("config on disk = %#v", reloaded.Views)
	}
}

func TestCheckoutFromDetailUsesShiftC(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.currentBranch = "main"
	pr := gh.PR{Number: 14, HeadRefName: "feature", BaseRefName: "main", HeadRefOID: "abc"}
	m.cache.PR = &pr

	// c still switches to the commits tab on the detail screen.
	m.active = conversationTab
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if got := u.(Model); got.active != commitsTab || got.pendingPRAction != noPRAction {
		t.Fatalf("c = tab:%v pending:%v", got.active, got.pendingPRAction)
	}

	// C asks to check the shown PR out.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = u.(Model)
	if m.pendingPRAction != checkoutPR || m.prActionNumber != 14 {
		t.Fatalf("C = pending:%v number:%d", m.pendingPRAction, m.prActionNumber)
	}
	if popup := ansi.Strip(m.renderActionPopup()); !strings.Contains(popup, "Checkout feature?") {
		t.Fatalf("confirm popup = %q", popup)
	}
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if got := u.(Model); got.prActionRunning != checkoutPR || cmd == nil {
		t.Fatalf("confirm = running:%v cmd:%v", got.prActionRunning, cmd)
	}

	// The PR already checked out offers nothing to do.
	current := testModel()
	current.screen, current.currentBranch = detailScreen, "feature"
	currentPR := gh.PR{Number: 14, HeadRefName: "feature"}
	current.cache.PR = &currentPR
	u, _ = current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	if got := u.(Model); got.pendingPRAction != noPRAction {
		t.Fatal("offered to check out the branch already checked out")
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

func TestBrowseURLAvailableOnEveryDetailTab(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	pr := gh.PR{Number: 7, URL: "https://example/pr/7"}
	m.cache.PR = &pr
	m.conversationDirty = true

	// c, f, i have no per-row link here (no checks are loaded), so they
	// offer the pull request itself.
	for _, tab := range []tab{commitsTab, conflictsTab, checksTab} {
		m.active = tab
		if got := m.selectedBrowseURL(); got != "https://example/pr/7" {
			t.Fatalf("tab %d URL = %q", tab, got)
		}
	}

	// A conversation row with its own URL still wins.
	comment := gh.Comment{ID: 1, Body: "hi", HTMLURL: "https://example/pr/7#c1"}
	comment.User.Login = "me"
	m.cache.Comments = []gh.Comment{comment}
	m.active = conversationTab
	m.conversationDirty = true
	items := m.conversationItems()
	for i, it := range items {
		if it.comment != nil {
			m.cursors[conversationTab] = i
		}
	}
	if got := m.selectedBrowseURL(); got != "https://example/pr/7#c1" {
		t.Fatalf("comment URL = %q", got)
	}
	// A row without one falls back rather than disabling the key.
	for i, it := range items {
		if it.activity != nil || it.event != nil {
			m.cursors[conversationTab] = i
			if got := m.selectedBrowseURL(); got != "https://example/pr/7" {
				t.Fatalf("row %d URL = %q", i, got)
			}
			break
		}
	}

	// Without a pull request there is still nothing to open.
	local := testModel()
	local.screen, local.active = detailScreen, checksTab
	if got := local.selectedBrowseURL(); got != "" {
		t.Fatalf("local-only detail URL = %q", got)
	}
}

func TestChecksTabBrowseURLOpensSelectedCheck(t *testing.T) {
	m := testModel()
	m.screen, m.active = detailScreen, checksTab
	m.cache.PR = &gh.PR{Number: 7, URL: "https://example/pr/7", Checks: []gh.PRCheck{
		{Name: "test", Conclusion: "FAILURE", DetailsURL: "https://example/runs/1"},
		{Context: "ci/legacy", State: "FAILURE", TargetURL: "https://example/status/2"},
		{Name: "pending", Status: "QUEUED"},
	}}

	// The selected row opens its own log page: detailsUrl for a check run,
	// targetUrl for a legacy status context.
	m.cursors[checksTab] = 0
	if got := m.selectedBrowseURL(); got != "https://example/runs/1" {
		t.Fatalf("check run URL = %q", got)
	}
	m.cursors[checksTab] = 1
	if got := m.selectedBrowseURL(); got != "https://example/status/2" {
		t.Fatalf("status context URL = %q", got)
	}
	// A check without a log page falls back to the pull request itself.
	m.cursors[checksTab] = 2
	if got := m.selectedBrowseURL(); got != "https://example/pr/7" {
		t.Fatalf("URL-less check fell back to %q", got)
	}
}

func TestCopyURLKeyOnListAndDetail(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.openPRs = []gh.PR{{Number: 7, URL: "https://example/pr/7", Title: "seven"}}
	m.prStacks = buildPRStacks(m.openPRs)
	m.prCursor = 0

	// y copies the selected row's URL without opening a browser.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y produced no command on the PR list")
	}
	if m.selectedBrowseURL() != "https://example/pr/7" {
		t.Fatalf("selected URL = %q", m.selectedBrowseURL())
	}
	// The result reports through browserDone, which the footer already words.
	done, _ := m.Update(browserDone{copied: true})
	if got := done.(Model).notice; got != "URL copied to clipboard" {
		t.Fatalf("notice = %q", got)
	}

	// A row without a URL (the synthetic local PR) copies nothing.
	local := testModel()
	local.screen = prListScreen
	local.openPRs = []gh.PR{{Number: 0, Title: "local"}}
	local.prStacks = buildPRStacks(local.openPRs)
	if local.copySelectedURL() != nil {
		t.Fatal("local PR offered a URL to copy")
	}

	// Detail screen: y copies the focused conversation item's URL.
	detail := testModel()
	detail.screen, detail.active = detailScreen, conversationTab
	detail.cache.PR = &gh.PR{Number: 7, URL: "https://example/pr/7"}
	detail.conversationDirty = true
	if url := detail.selectedBrowseURL(); url == "" {
		t.Fatal("detail conversation exposed no URL")
	}
	if _, cmd := detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}); cmd == nil {
		t.Fatal("y produced no command on the detail screen")
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
	m.base, m.head = "origin/main", "feat/x"
	if header := ansi.Strip(m.renderHeader()); !strings.Contains(header, "origin/main ← feat/x") {
		t.Fatalf("detail header = %q", header)
	}
	list := testModel()
	list.defaultBranch = "main"
	list.screen, list.w = prListScreen, 120
	list.openPRs = []gh.PR{{Number: 1, Title: "one", BaseRefName: "main", HeadRefName: "feat/x", URL: "u"}}
	list.prStacks = buildPRStacks(list.openPRs)
	if preview := ansi.Strip(list.buildPRPreview()); !strings.Contains(preview, "main ← feat/x") {
		t.Fatalf("preview = %q", preview)
	}
}

func TestModalPopupsDoNotDropDetailAsyncCompletions(t *testing.T) {
	// localLoaded arrives while the status popup is open.
	m := testModel()
	m.refreshing = true
	m.statusPR = gh.PR{Number: 12, State: "OPEN"}
	u, _ := m.Update(localLoaded{generation: m.targetGeneration, err: errors.New("load failed")})
	if got := u.(Model); got.refreshing {
		t.Fatal("localLoaded was dropped by the status popup")
	}

	// checkoutReloaded arrives while the view manager is open.
	m = testModel()
	m.refreshing = true
	m.viewManager = true
	u, _ = m.Update(checkoutReloaded{generation: m.targetGeneration, number: 7, err: errors.New("checkout reload: boom")})
	if got := u.(Model); got.refreshing {
		t.Fatal("checkoutReloaded was dropped by the view manager")
	}

	// rawDetailLoaded arrives while the editor overlay is open.
	m = testModel()
	m.localEditMode = addLocalComment
	m.rawPending = map[string]bool{"k": true}
	m.rawDetailCache = map[string]string{}
	u, _ = m.Update(rawDetailLoaded{generation: m.targetGeneration, key: "k", raw: "diff"})
	if got := u.(Model); got.rawPending["k"] || got.rawDetailCache["k"] != "diff" {
		t.Fatal("rawDetailLoaded was dropped by the editor overlay")
	}

	// baseResolved arrives while the delete confirm is open.
	m = testModel()
	m.localDeleteTarget = "c1"
	u, _ = m.Update(baseResolved{
		generation: m.targetGeneration, base: m.base, diffBase: m.diffBase,
		files: []git.ChangedFile{{Status: "A", Path: "new.go"}},
	})
	if got := u.(Model); len(got.files) != 1 || got.files[0].Path != "new.go" {
		t.Fatal("baseResolved was dropped by the delete confirm")
	}
}

func TestStaleCheckoutReloadCannotReplaceModel(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.targetGeneration = 2
	next := testModel()
	next.screen = prListScreen
	u, _ := m.Update(checkoutReloaded{generation: 1, number: 9, next: &next})
	got := u.(Model)
	if got.screen != detailScreen || got.notice != "" {
		t.Fatalf("stale checkout reload replaced the model: screen=%v notice=%q", got.screen, got.notice)
	}
}

func TestStalePRPreviewResultClearsLoadingTicket(t *testing.T) {
	m := testModel()
	m.prListGeneration = 2
	m.prPreviewLoading = map[int]bool{7: true}
	u, _ := m.Update(prPreviewLoaded{generation: 1, number: 7, pr: gh.PR{Number: 7}})
	got := u.(Model)
	if len(got.prPreviewLoading) != 0 {
		t.Fatalf("stale preview result orphaned the loading ticket: %#v", got.prPreviewLoading)
	}
	if got.isLoading() {
		t.Fatal("orphaned ticket kept the spinner alive")
	}
	if got.prPreviewLoaded[7] {
		t.Fatal("stale preview result was applied")
	}
}

func TestPRListHelpKeyTogglesFullHelp(t *testing.T) {
	m := testModel()
	m.screen = prListScreen

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = u.(Model)
	if !m.help.ShowAll {
		t.Fatal("? on the PR list did not open the full help")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if u.(Model).help.ShowAll {
		t.Fatal("second ? on the PR list did not close the full help")
	}
}
