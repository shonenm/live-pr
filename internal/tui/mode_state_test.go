package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

// All state, Git repositories, and config are disposable. No returned command
// that talks to GitHub or starts the embedded reviewer is executed.
func modeFixture(t *testing.T) (*Model, *store.Store) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root := t.TempDir()
	t.Chdir(root)
	modeGit(t, "init", "-b", "main")
	modeGit(t, "config", "user.name", "Audit")
	modeGit(t, "config", "user.email", "audit@example.invalid")
	modeWrite(t, "clean\n")
	modeGit(t, "add", "tracked.txt")
	modeGit(t, "commit", "-m", "base")
	base := modeGit(t, "rev-parse", "HEAD")
	modeGit(t, "switch", "-c", "feature")
	modeGit(t, "commit", "--allow-empty", "-m", "feature")
	head := modeGit(t, "rev-parse", "HEAD")
	cache := gh.NewCache("feature")
	cache.PR = &gh.PR{Number: 10, State: "OPEN", Title: "Current checkout", HeadRefName: "feature", HeadRefOID: head, BaseRefName: "main", BaseRefOID: base}
	st := store.ForBranch(root, "feature")
	data, err := loadLocalData(st, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.root, m.currentBranch, m.defaultBranch = root, "feature", "main"
	m.pollTimers = &pollTimers{}
	m.navigatorPath = store.NavigatorCache(root)
	m.applyLocal(st, data)
	m.refreshing = false
	t.Cleanup(m.close)
	if m.detailMode() != modeLive {
		t.Fatalf("fixture is not LIVE: %v", m.detailMode())
	}
	return &m, st
}

func modeGit(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func modeWrite(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile("tracked.txt", []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func pollModeFixture(t *testing.T, m *Model) {
	t.Helper()
	state, err := git.CurrentLocalState()
	if err != nil {
		t.Fatal(err)
	}
	next, cmd := m.handleLocalStatePolled(localStatePolled{generation: m.localGeneration, state: state})
	*m = next
	if !m.localReloading {
		return
	}
	if cmd == nil {
		t.Fatal("reload without command")
	}
	*m, _ = m.handleLocalLoaded(localLoadResult(t, cmd))
}

func TestModeConversationRefreshKeepsCoherentLocalSnapshot(t *testing.T) {
	m, _ := modeFixture(t)
	cleanFingerprint := m.localFingerprint
	modeWrite(t, "transient edit\n")
	*m, _ = m.handleGitHubConversationRefreshed(githubConversationRefreshed{generation: m.targetGeneration, number: 10})
	if m.workingTreeDirty || m.localFingerprint != cleanFingerprint {
		t.Fatal("conversation must not partially replace the local snapshot")
	}
	modeWrite(t, "clean\n")
	if got := modeGit(t, "status", "--short"); got != "" {
		t.Fatalf("not clean: %q", got)
	}
	current, err := git.CurrentLocalState()
	if err != nil {
		t.Fatal(err)
	}
	if current.Fingerprint != cleanFingerprint {
		t.Fatal("setup: fingerprint did not return to baseline")
	}
	pollModeFixture(t, m)
	t.Logf("git status clean; fingerprint unchanged=%v; dirty=%v; mode=%s", m.localFingerprint == current.Fingerprint, m.workingTreeDirty, m.dataModeLabel())
	if m.detailMode() != modeLive || m.workingTreeDirty || m.worktreeSummary.Total() != 0 {
		t.Fatal("clean checkout retained stale worktree state after a successful poll")
	}
}

func TestModeDirtyReloadKeepsLiveCIPolling(t *testing.T) {
	m, _ := modeFixture(t)
	cancelled := false
	m.pollTimers.ci = func() { cancelled = true }
	modeWrite(t, "dirty\n")
	pollModeFixture(t, m)
	if !cancelled || m.detailMode() != modeLive {
		t.Fatal("setup: dirty reload did not reconcile LIVE polling")
	}
	modeWrite(t, "clean\n")
	pollModeFixture(t, m)
	if m.detailMode() != modeLive {
		t.Fatal("setup: clean reload did not restore LIVE")
	}
	t.Logf("returned to LIVE; CI timer installed=%v; local timer installed=%v", m.pollTimers.ci != nil, m.pollTimers.local != nil)
	if m.pollTimers.ci == nil {
		t.Fatal("LIVE never rearms its GitHub/CI polling after local reload")
	}
}

func TestModeLocalReloadPreservesDiscoveredPR(t *testing.T) {
	m, _ := modeFixture(t)
	knownPR := *m.cache.PR
	m.cache.PR = nil
	m.localFingerprint = "previous snapshot"
	state, err := git.CurrentLocalState()
	if err != nil {
		t.Fatal(err)
	}
	next, cmd := m.handleLocalStatePolled(localStatePolled{generation: m.localGeneration, state: state})
	*m = next
	if !m.localReloading || cmd == nil {
		t.Fatal("setup: expected local load")
	}
	loaded := localLoadResult(t, cmd)
	// The independent PR-list request resolves before the local load is applied.
	*m, _ = m.handlePRListRefreshed(prListRefreshed{generation: m.prList.generation, key: m.prList.activePage, page: gh.PRPage{PRs: []gh.PR{knownPR}}})
	if m.cache.PR == nil || m.detailMode() != modeLive {
		t.Fatal("setup: PR discovery did not establish LIVE")
	}
	next, retry := m.handleLocalLoaded(loaded)
	*m = next
	if m.cache.PR == nil || retry == nil {
		t.Fatal("new publication boundary was lost instead of rescanned")
	}
	*m, _ = m.handleLocalLoaded(localLoadResult(t, retry))
	t.Logf("after late local result: pr=%v; dirty=%v; mode=%s; refresh=%v", m.cache.PR, m.workingTreeDirty, m.dataModeLabel(), m.refreshing)
	if m.cache.PR == nil || m.detailMode() != modeLive {
		t.Fatal("older local cache snapshot erased a successfully discovered PR")
	}
}

func TestModeCheckoutChangedWhileListOpen(t *testing.T) {
	for _, target := range []string{"new checkout", "old checkout"} {
		t.Run(target, func(t *testing.T) {
			m, _ := modeFixture(t)
			oldPR := *m.cache.PR
			*m, _ = m.handleDetailKey(keyPress("b"))
			modeGit(t, "switch", "-c", "other")
			currentPR := gh.PR{Number: 20, State: "OPEN", HeadRefName: "other", HeadRefOID: oldPR.HeadRefOID, BaseRefName: "main", BaseRefOID: oldPR.BaseRefOID}
			selected := currentPR
			if target == "old checkout" {
				selected = oldPR
			}
			m.prList.open, m.prList.cursor = []gh.PR{selected}, 0
			next, cmd := m.openSelectedPR()
			*m = next
			*m, _ = m.handlePRTargetPrepared(preparedTargetResult(t, cmd))
			actual, err := git.CurrentBranch()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("actual checkout=%s; remembered checkout=%s; selected=%s; remote=%v; screen=%v", actual, m.currentBranch, selected.HeadRefName, m.remote, m.screen)
			if target == "new checkout" && m.remote {
				t.Fatal("the actual checkout is opened as REMOTE")
			}
			if target == "old checkout" && !m.remote {
				t.Fatal("an un-checked-out PR is opened through local hydration")
			}
		})
	}
}

func TestModeImplicitClosedCacheYieldsToNewOpenPR(t *testing.T) {
	m, st := modeFixture(t)
	closed := m.cache.Clone()
	closed.PR.State = "CLOSED"
	closed.ExplicitCheckout = false
	if err := gh.SaveCache(st.GitHubCache(), closed); err != nil {
		t.Fatal(err)
	}
	latest := *m.cache.PR
	latest.Number, latest.Title = 20, "New open PR on the same branch"
	nav := gh.NewNavigatorCache()
	nav.PRs = []gh.PR{latest}
	if err := gh.SaveNavigatorCache(m.navigatorPath, nav); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rebuilt.close)
	rebuilt.diffCommand, rebuilt.diffCommitCommand = "", ""
	if rebuilt.diffTerminal != nil {
		rebuilt.diffTerminal.Close()
		rebuilt.diffTerminal = nil
	}
	rebuilt.screen, rebuilt.prList.open, rebuilt.prList.cursor = prListScreen, []gh.PR{latest}, 0
	rebuilt, cmd := rebuilt.openSelectedPR()
	rebuilt, _ = rebuilt.handlePRTargetPrepared(preparedTargetResult(t, cmd))
	t.Logf("implicit cached PR=%d; open PR=%d; selected-open-PR remote=%v", closed.PR.Number, latest.Number, rebuilt.remote)
	if rebuilt.remote {
		t.Fatal("a stale implicit CLOSED association prevents the current OPEN PR from being local")
	}
}

func TestModeListLabelDoesNotClaimRemoteCheckout(t *testing.T) {
	m, _ := modeFixture(t)
	before := m.dataModeLabel()
	*m, _ = m.handleDetailKey(keyPress("b"))
	t.Logf("before b=%s; after b label=%s; detailMode=%v; remote=%v; PR=%d", before, m.dataModeLabel(), m.detailMode(), m.remote, m.currentPRNumber())
	if m.remote || m.detailMode() != modeLive || m.dataModeLabel() != "PR LIST" {
		t.Fatal("list label must be independent of the checkout mode")
	}
	if m.dataModeContext() != "checkout feature" {
		t.Fatalf("list context leaked the previously reviewed PR: %q", m.dataModeContext())
	}
}

func TestModePublishedRangeStaysLiveWithDirtyWorktree(t *testing.T) {
	m, _ := modeFixture(t)
	before := m.detailView.reviewRange
	modeWrite(t, "dirty\n")
	pollModeFixture(t, m)
	t.Logf("mode=%s; published range unchanged=%v; dirty=%v", m.dataModeLabel(), m.detailView.reviewRange == before, m.workingTreeDirty)
	if m.detailMode() != modeLive || !m.workingTreeDirty || m.detailView.reviewRange != before {
		t.Fatal("dirty state must not change a published PR review target")
	}
}

func localLoadResult(t *testing.T, cmd tea.Cmd) localLoaded {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		// handleLocalStatePolled batches the local load before the spinner.
		msg = batch[0]()
	}
	loaded, ok := msg.(localLoaded)
	if !ok {
		t.Fatalf("expected localLoaded, got %T", msg)
	}
	if loaded.err != nil {
		t.Fatal(loaded.err)
	}
	return loaded
}

func TestModeLiveDoesNotRequirePublishedHeadEquality(t *testing.T) {
	m, _ := modeFixture(t)
	modeGit(t, "commit", "--allow-empty", "-m", "local unpublished commit")
	pollModeFixture(t, m)
	t.Logf("mode=%s; local HEAD equals PR head=%v; dirty=%v", m.dataModeLabel(), m.localHeadOID == m.cache.PR.HeadRefOID, m.workingTreeDirty)
	if m.detailMode() != modeLive || m.localHeadOID == m.cache.PR.HeadRefOID {
		t.Fatal("unpublished commits must not change the PR review target")
	}
}

func preparedTargetResult(t *testing.T, cmd tea.Cmd) prTargetPrepared {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		msg = batch[0]()
	}
	prepared, ok := msg.(prTargetPrepared)
	if !ok {
		t.Fatalf("expected prTargetPrepared, got %T", msg)
	}
	if prepared.err != nil {
		t.Fatal(prepared.err)
	}
	return prepared
}

func TestModeLocalEntryDoesNotBecomeLive(t *testing.T) {
	m, _ := modeFixture(t)
	m.cache = gh.NewCache(m.currentBranch)
	m.navigator.PRs = nil
	m.screen, m.prList.open, m.prList.cursor = prListScreen, []gh.PR{{State: "LOCAL", HeadRefName: "feature"}}, 0
	next, cmd := m.openSelectedPR()
	*m = next
	next, cmd = m.handlePRTargetPrepared(preparedTargetResult(t, cmd))
	*m = next
	*m, _ = m.handleLocalLoaded(localLoadResult(t, cmd))
	if m.cache.PR != nil || m.remote || m.detailMode() != modeLocal {
		t.Fatalf("local-only entry acquired a PR: pr=%v remote=%v mode=%s", m.cache.PR, m.remote, m.dataModeLabel())
	}
}

func TestModeExplicitCheckoutPinWinsOverNewOpenPR(t *testing.T) {
	m, st := modeFixture(t)
	m.cache.PR.State, m.cache.ExplicitCheckout = "CLOSED", true
	if err := gh.SaveCache(st.GitHubCache(), m.cache); err != nil {
		t.Fatal(err)
	}
	newer := *m.cache.PR
	newer.Number, newer.State = 20, "OPEN"
	m.screen, m.prList.open, m.prList.cursor = prListScreen, []gh.PR{newer}, 0
	next, cmd := m.openSelectedPR()
	*m = next
	*m, _ = m.handlePRTargetPrepared(preparedTargetResult(t, cmd))
	if !m.remote || m.checkoutCache.PR.Number != 10 || !m.checkoutCache.ExplicitCheckout {
		t.Fatal("opening another PR silently replaced an explicit checkout pin")
	}
}

func TestModeLocalReloadPreservesFreshGitHubFields(t *testing.T) {
	m, st := modeFixture(t)
	m.localReloading = true
	loaded := localLoadResult(t, m.startLocalLoad(st, m.cache, m.cache.PR))
	m.cache.PR.Title = "new title"
	m.cache.PR.State = "CLOSED"
	m.cache.Comments = []gh.Comment{{ID: 42, Body: "new comment"}}
	m.cache.ExplicitCheckout = true
	*m, _ = m.handleLocalLoaded(loaded)
	if m.cache.PR.Title != "new title" || m.cache.PR.State != "CLOSED" || len(m.cache.Comments) != 1 || m.cache.Comments[0].ID != 42 || !m.cache.ExplicitCheckout {
		t.Fatal("local hydration rolled back GitHub state or checkout identity")
	}
}

func TestModeCheckoutChangeDuringLocalLoadIsRejected(t *testing.T) {
	m, st := modeFixture(t)
	cmd := m.startLocalLoad(st, m.cache, m.cache.PR)
	modeGit(t, "switch", "-c", "other")
	loaded := cmd().(localLoaded)
	if loaded.err == nil || !strings.Contains(loaded.err.Error(), "checkout changed") {
		t.Fatalf("mixed checkout data was accepted: %v", loaded.err)
	}
	*m, _ = m.handleLocalLoaded(loaded)
	if m.currentBranch != "feature" || m.cache.PR.Number != 10 || m.detailView.head != "feature" {
		t.Fatal("failed hydration replaced the displayed target")
	}
}

func TestModeCheckoutWatcherSurvivesScreenAndDetailChanges(t *testing.T) {
	m, _ := modeFixture(t)
	for _, screen := range []screen{prListScreen, detailScreen} {
		for _, remote := range []bool{false, true} {
			m.screen, m.remote, m.refreshing = screen, remote, true
			m.targetGeneration += 10
			if _, cmd := m.Update(localPollTick{generation: m.localGeneration}); cmd == nil {
				t.Fatalf("checkout observer stopped: screen=%v remote=%v", screen, remote)
			}
		}
	}
	if _, cmd := m.Update(localPollTick{generation: m.localGeneration + 1}); cmd != nil {
		t.Fatal("stale checkout epoch was accepted")
	}
}

func TestModeExternalCheckoutPreservesScreenAndReviewTarget(t *testing.T) {
	for _, view := range []string{"list", "live", "remote"} {
		t.Run(view, func(t *testing.T) {
			m, _ := modeFixture(t)
			if view == "list" {
				*m, _ = m.handleDetailKey(keyPress("b"))
			}
			if view == "remote" {
				_ = m.openRemote(gh.PR{Number: 20, State: "OPEN", HeadRefName: "remote", BaseRefName: "main"})
			}
			selected := m.currentPRNumber()
			modeGit(t, "switch", "-c", "other")
			state, err := git.CurrentLocalState()
			if err != nil {
				t.Fatal(err)
			}
			next, cmd := m.handleLocalStatePolled(localStatePolled{generation: m.localGeneration, state: state})
			*m = next
			if !m.checkoutReloading {
				t.Fatal("checkout change was not observed")
			}
			batch := cmd().(tea.BatchMsg)
			loaded := batch[0]().(localBranchReloaded)
			if loaded.err != nil {
				t.Fatal(loaded.err)
			}
			*m, _ = m.handleLocalBranchReloaded(loaded)
			if m.currentBranch != "other" {
				t.Fatal("checkout identity remained stale")
			}
			if view == "list" {
				if m.screen != prListScreen || m.dataModeLabel() != "PR LIST" || m.autoOpenCurrent {
					t.Fatal("background checkout forced navigation")
				}
			} else if m.screen != detailScreen || !m.remote || m.currentPRNumber() != selected {
				t.Fatal("background checkout silently replaced the reviewed PR")
			}
		})
	}
}

func TestModePreparedSelectionRejectsLateTarget(t *testing.T) {
	m, _ := modeFixture(t)
	*m, _ = m.handleDetailKey(keyPress("b"))
	m.prList.open = []gh.PR{{Number: 20, HeadRefName: "a", BaseRefName: "main"}, {Number: 30, HeadRefName: "b", BaseRefName: "main"}}
	m.prList.cursor = 0
	next, cmd := m.openSelectedPR()
	*m = next
	stale := preparedTargetResult(t, cmd)
	m.prList.cursor = 1
	next, cmd = m.openSelectedPR()
	*m = next
	*m, _ = m.handlePRTargetPrepared(stale)
	if m.remote || m.currentPRNumber() != 10 {
		t.Fatal("old selection replaced the checkout")
	}
	*m, _ = m.handlePRTargetPrepared(preparedTargetResult(t, cmd))
	if !m.remote || m.currentPRNumber() != 30 {
		t.Fatal("latest selection was not applied")
	}
}
