package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/internal/github"
)

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

func TestPRListActionsRequireConfirmation(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.prList.open = []gh.PR{{Number: 14, HeadRefName: "feature", HeadRefOID: "abc123"}}
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

	// j moves the cursor to Squash and the prompt follows the selection.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	popup := ansi.Strip(m.renderActionPopup())
	if m.mergeMethodCursor != 1 || !strings.Contains(popup, "Squash and merge?") || !strings.Contains(popup, "Rebase") {
		t.Fatalf("squash selection not shown: cursor=%d popup=%q", m.mergeMethodCursor, popup)
	}
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd == nil || m.pendingPRAction != noPRAction || m.prActionRunning != mergePR {
		t.Fatalf("enter did not fire the merge: pending=%v running=%v", m.pendingPRAction, m.prActionRunning)
	}
	runInner(cmd)
	if gotNumber != 14 || gotOID != "abc123" || gotMethod != gh.MergeSquash {
		t.Fatalf("Merge(%d, %q, %q); want squash for PR #14 at abc123", gotNumber, gotOID, gotMethod)
	}

	// Reopening resets the cursor to the merge commit; r submits a rebase.
	m.prActionRunning = noPRAction
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = u.(Model)
	if m.mergeMethodCursor != 0 || !strings.Contains(ansi.Strip(m.renderActionPopup()), "Merge with a merge commit?") {
		t.Fatalf("reopened popup did not reset to the merge commit: cursor=%d", m.mergeMethodCursor)
	}
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = u.(Model)
	runInner(cmd)
	if m.prActionRunning != mergePR || gotMethod != gh.MergeRebase {
		t.Fatalf("r shortcut = running:%v method:%q; want an immediate rebase merge", m.prActionRunning, gotMethod)
	}

	// Esc cancels without touching the client.
	m.prActionRunning = noPRAction
	gotNumber, gotMethod = 0, ""
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = u.(Model)
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if cmd != nil || m.pendingPRAction != noPRAction || m.prActionNumber != 0 || gotNumber != 0 {
		t.Fatalf("esc did not cancel: pending=%v number=%d merged=%d", m.pendingPRAction, m.prActionNumber, gotNumber)
	}
}

func TestPRListActionsAreDisabledForLocalEntry(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.prList.open = []gh.PR{{Title: "Local PR"}}
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

func TestCheckoutFromDetailUsesShiftC(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.currentBranch = "main"
	pr := gh.PR{Number: 14, HeadRefName: "feature", BaseRefName: "main", HeadRefOID: "abc"}
	m.cache.PR = &pr

	// c still switches to the commits tab on the detail screen.
	m.detailView.active = conversationTab
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if got := u.(Model); got.detailView.active != commitsTab || got.pendingPRAction != noPRAction {
		t.Fatalf("c = tab:%v pending:%v", got.detailView.active, got.pendingPRAction)
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
