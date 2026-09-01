package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
)

func TestMergePopupShowsMergeConditions(t *testing.T) {
	// A clean PR shows every condition as ok.
	m := testModel()
	m.screen = detailScreen
	pr := gh.PR{
		Number: 9, State: "OPEN", HeadRefOID: "head",
		Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN",
		ReviewDecision: "APPROVED", PreviewLoaded: true,
		Checks: []gh.PRCheck{{Name: "unit", Status: "COMPLETED", Conclusion: "SUCCESS"}},
	}
	m.cache.PR = &pr
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(keyPress("m"))
	m = u.(Model)
	popup := ansi.Strip(m.renderActionPopup())
	for _, want := range []string{"⇄ mergeable", "✓ CI 1 passed", "review approved", "✓ up to date with base"} {
		if !strings.Contains(popup, want) {
			t.Fatalf("clean merge popup missing %q: %q", want, popup)
		}
	}

	// A dirty PR shows each blocked condition as a warning, including the
	// local behind/conflict scan for the PR loaded on the detail screen.
	m = testModel()
	m.screen = detailScreen
	pr = gh.PR{
		Number: 9, State: "OPEN", HeadRefOID: "head", IsDraft: true,
		Mergeable:      "CONFLICTING",
		ReviewDecision: "CHANGES_REQUESTED", PreviewLoaded: true,
		Checks: []gh.PRCheck{
			{Name: "unit", Status: "COMPLETED", Conclusion: "FAILURE"},
			{Name: "lint", Status: "IN_PROGRESS"},
		},
	}
	m.cache.PR = &pr
	m.detailView.mergeReadiness = git.MergeReadiness{Behind: 2, ConflictFiles: []string{"a.go"}}
	m.pendingPRAction, m.prActionNumber, m.prActionPR = mergePR, 9, pr
	u, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	popup = ansi.Strip(m.renderActionPopup())
	for _, want := range []string{"◌ draft", "⚠ conflicts", "✗ CI 1 failed", "1 pending", "review changes requested", "⚠ 1 conflict file", "⚠ 2 commits behind base"} {
		if !strings.Contains(popup, want) {
			t.Fatalf("dirty merge popup missing %q: %q", want, popup)
		}
	}

	// The local behind/conflict scan belongs to the detail target only, so a
	// popup opened from the list omits it.
	m.screen = prListScreen
	popup = ansi.Strip(m.renderActionPopup())
	if strings.Contains(popup, "behind base") || strings.Contains(popup, "conflict file") {
		t.Fatalf("list merge popup leaked detail readiness: %q", popup)
	}
}

func TestDetailMergeStartsConfirmation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.cache.PR = &gh.PR{Number: 9, State: "OPEN", HeadRefOID: "head", HeadRefName: "feature/x"}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, cmd := m.Update(keyPress("m"))
	m = u.(Model)
	if cmd != nil || m.pendingPRAction != mergePR || m.prActionNumber != 9 {
		t.Fatalf("detail merge confirmation = pending:%v number:%d cmd:%v", m.pendingPRAction, m.prActionNumber, cmd)
	}
}

func TestPRListActionsRequireConfirmation(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.prList.open = []gh.PR{{Number: 14, HeadRefName: "feature", HeadRefOID: "abc123"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)

	u, cmd := m.Update(keyPress("m"))
	m = u.(Model)
	if cmd != nil || m.pendingPRAction != mergePR || m.prActionPR.HeadRefOID != "abc123" || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Merge PR #14") || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Squash and merge?") || !strings.Contains(ansi.Strip(m.viewContent()), "Merge PR #14") {
		t.Fatalf("merge confirmation not shown: pending=%v popup=%q", m.pendingPRAction, ansi.Strip(m.renderActionPopup()))
	}
	u, _ = m.Update(keyPress("n"))
	m = u.(Model)
	if m.pendingPRAction != noPRAction {
		t.Fatalf("merge confirmation not cancelled: %v", m.pendingPRAction)
	}

	u, _ = m.Update(keyPress("c"))
	m = u.(Model)
	u, cmd = m.Update(keyPress("y"))
	m = u.(Model)
	if cmd == nil || m.pendingPRAction != noPRAction || m.prActionRunning != checkoutPR || m.prActionNumber != 14 {
		t.Fatalf("checkout not confirmed: pending=%v running=%v number=%d cmd=%v", m.pendingPRAction, m.prActionRunning, m.prActionNumber, cmd)
	}

	m.prActionRunning = noPRAction
	u, _ = m.Update(keyPress("x"))
	m = u.(Model)
	if m.pendingPRAction != closePR || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Close PR #14") || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Close without merging") {
		t.Fatalf("close confirmation not shown: pending=%v popup=%q", m.pendingPRAction, ansi.Strip(m.renderActionPopup()))
	}
}

func TestMergePopupPicksMethodWithCursorAndShortcuts(t *testing.T) {
	var gotNumber int
	var gotOID string
	var gotMethod gh.MergeMethod
	m := testModel()
	m.client = &fakeGH{merge: func(number int, headOID string, method gh.MergeMethod) error {
		gotNumber, gotOID, gotMethod = number, headOID, method
		return nil
	}}
	m.screen = prListScreen
	m.currentBranch = "main"
	m.prList.open = []gh.PR{{Number: 14, HeadRefName: "feature", HeadRefOID: "abc123"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	runInner := func(cmd tea.Cmd) {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, inner := range batch {
				inner()
			}
		}
	}

	// Squash is selected by default.
	u, _ = m.Update(keyPress("m"))
	m = u.(Model)
	popup := ansi.Strip(m.renderActionPopup())
	if m.mergeMethodCursor != 0 || !strings.Contains(popup, "Squash and merge?") || !strings.Contains(popup, "Rebase") {
		t.Fatalf("squash selection not shown: cursor=%d popup=%q", m.mergeMethodCursor, popup)
	}
	u, cmd := m.Update(keyPress("enter"))
	m = u.(Model)
	if cmd == nil || m.pendingPRAction != noPRAction || m.prActionRunning != mergePR {
		t.Fatalf("enter did not fire the merge: pending=%v running=%v", m.pendingPRAction, m.prActionRunning)
	}
	runInner(cmd)
	if gotNumber != 14 || gotOID != "abc123" || gotMethod != gh.MergeSquash {
		t.Fatalf("Merge(%d, %q, %q); want squash for PR #14 at abc123", gotNumber, gotOID, gotMethod)
	}

	// Reopening resets the cursor to squash; r still submits a rebase directly.
	m.prActionRunning = noPRAction
	u, _ = m.Update(keyPress("m"))
	m = u.(Model)
	if m.mergeMethodCursor != 0 || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Squash and merge?") {
		t.Fatalf("reopened popup did not reset to squash: cursor=%d", m.mergeMethodCursor)
	}
	u, cmd = m.Update(keyPress("r"))
	m = u.(Model)
	runInner(cmd)
	if m.prActionRunning != mergePR || gotMethod != gh.MergeRebase {
		t.Fatalf("r shortcut = running:%v method:%q; want an immediate rebase merge", m.prActionRunning, gotMethod)
	}

	// Esc cancels without touching the client.
	m.prActionRunning = noPRAction
	gotNumber, gotMethod = 0, ""
	u, _ = m.Update(keyPress("m"))
	m = u.(Model)
	u, cmd = m.Update(keyPress("esc"))
	m = u.(Model)
	if cmd != nil || m.pendingPRAction != noPRAction || m.prActionNumber != 0 || gotNumber != 0 {
		t.Fatalf("esc did not cancel: pending=%v number=%d merged=%d", m.pendingPRAction, m.prActionNumber, gotNumber)
	}
}

func TestMergePopupUsesConfiguredMethodOrder(t *testing.T) {
	m := testModel()
	m.mergeMethods = configuredMergeMethods([]string{"rebase", "squash", "merge"})
	m.pendingPRAction, m.prActionNumber = mergePR, 14
	m.prActionPR = gh.PR{Number: 14, HeadRefOID: "abc123"}

	popup := ansi.Strip(m.renderActionPopup())
	squash, merge, rebase := strings.Index(popup, "Squash"), strings.Index(popup, "Merge commit"), strings.Index(popup, "Rebase")
	if m.selectedMergeMethod() != gh.MergeRebase || rebase < 0 || squash < rebase || merge < squash || !strings.Contains(popup, "Rebase and merge?") {
		t.Fatalf("configured merge picker = cursor:%d method:%q popup:%q", m.mergeMethodCursor, m.selectedMergeMethod(), popup)
	}
}

func TestPRListActionsAreDisabledForLocalEntry(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.prList.open = []gh.PR{{Title: "Local PR"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	for _, action := range []rune{'m', 'c', 'x'} {
		u, cmd := m.Update(keyPress(string(action)))
		m = u.(Model)
		if cmd != nil || m.pendingPRAction != noPRAction {
			t.Fatalf("local action %q was enabled", action)
		}
	}
}

func TestCheckoutFromDetailUsesShiftC(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.currentBranch = "main"
	pr := gh.PR{Number: 14, HeadRefName: "feature", BaseRefName: "main", HeadRefOID: "abc"}
	m.cache.PR = &pr

	// c still switches to the commits tab on the detail screen.
	m.detailView.active = conversationTab
	u, _ := m.Update(keyPress("c"))
	if got := u.(Model); got.detailView.active != commitsTab || got.pendingPRAction != noPRAction {
		t.Fatalf("c = tab:%v pending:%v", got.detailView.active, got.pendingPRAction)
	}

	// C asks to check the shown PR out.
	u, _ = m.Update(keyPress("C"))
	m = u.(Model)
	if m.pendingPRAction != checkoutPR || m.prActionNumber != 14 {
		t.Fatalf("C = pending:%v number:%d", m.pendingPRAction, m.prActionNumber)
	}
	if popup := ansi.Strip(m.renderActionPopup()); !strings.Contains(popup, "Checkout feature?") {
		t.Fatalf("confirm popup = %q", popup)
	}
	u, cmd := m.Update(keyPress("y"))
	if got := u.(Model); got.prActionRunning != checkoutPR || cmd == nil {
		t.Fatalf("confirm = running:%v cmd:%v", got.prActionRunning, cmd)
	}

	// The PR already checked out offers nothing to do.
	current := testModel()
	current.screen, current.currentBranch = detailScreen, "feature"
	currentPR := gh.PR{Number: 14, HeadRefName: "feature"}
	current.cache.PR = &currentPR
	u, _ = current.Update(keyPress("C"))
	if got := u.(Model); got.pendingPRAction != noPRAction {
		t.Fatal("offered to check out the branch already checked out")
	}
}

func TestOverlayPopupKeepsAlignmentOverWideRunes(t *testing.T) {
	// Every base cell is a double-width rune, so whatever origin the popup
	// gets, both cut boundaries straddle a rune and used to shear the rows.
	wide := strings.Repeat("あ", 20) // 40 cells
	base := strings.Join([]string{wide, wide, wide, wide, wide}, "\n")
	popup := strings.Join([]string{"+-----+", "| box |", "+-----+"}, "\n")
	merged := overlayPopup(base, popup, 40)
	for i, line := range strings.Split(merged, "\n") {
		if got := lipgloss.Width(line); got != 40 {
			t.Fatalf("line %d width = %d, want 40: %q", i, got, line)
		}
	}
	if !strings.Contains(merged, "| box |") {
		t.Fatalf("popup content lost: %q", merged)
	}
	// The popup's columns must be identical on every popup row: a shear shows
	// up as differing indent between the box's top and middle lines.
	var cols []int
	for _, line := range strings.Split(ansi.Strip(merged), "\n") {
		if i := strings.IndexAny(line, "+|"); i >= 0 {
			cols = append(cols, lipgloss.Width(line[:i]))
		}
	}
	if len(cols) != 3 || cols[0] != cols[1] || cols[1] != cols[2] {
		t.Fatalf("popup columns shifted between rows: %v", cols)
	}
}
