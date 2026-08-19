package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/store"
)

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
