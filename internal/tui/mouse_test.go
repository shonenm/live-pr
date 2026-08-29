// Mouse interaction: pane-aware wheel scrolling, click-to-select semantics,
// header tab clicks, and popup mouse handling.
package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prfilter"
)

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheel(x, y int, down bool) tea.MouseWheelMsg {
	button := tea.MouseWheelUp
	if down {
		button = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

// listMouseModel is a sized PR-list screen with n plain (unstacked) PRs, so
// PR i sits at content lines 3i and 3i+1 with line 3i+2 as the separator.
func listMouseModel(t *testing.T, n, width, height int) Model {
	t.Helper()
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	for i := 1; i <= n; i++ {
		m.prList.open = append(m.prList.open, gh.PR{Number: i, Title: "PR", HeadRefName: "feature", BaseRefName: "main", HeadRefOID: "abc123"})
	}
	m.prList.stacks = prfilter.BuildStacks(m.prList.open)
	u, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return u.(Model)
}

func TestPRListClickSelectsAndSecondClickOpens(t *testing.T) {
	m := listMouseModel(t, 3, 120, 30)
	contentTop := m.headerHeight() + 1

	// Click on the third PR's first line selects it.
	u, _ := m.Update(click(5, contentTop+6))
	m = u.(Model)
	if m.prList.cursor != 2 {
		t.Fatalf("click on row 2 moved cursor to %d", m.prList.cursor)
	}
	// The blank separator line and padding past the last row do nothing.
	for _, y := range []int{contentTop + 2, contentTop + 8, contentTop + 15} {
		u, _ = m.Update(click(5, y))
		m = u.(Model)
		if m.prList.cursor != 2 || m.screen != prListScreen {
			t.Fatalf("miss at y=%d changed state: cursor=%d screen=%v", y, m.prList.cursor, m.screen)
		}
	}
	// Clicking the already-selected row opens it — the Enter equivalent.
	u, _ = m.Update(click(5, contentTop+6))
	m = u.(Model)
	if m.screen != detailScreen {
		t.Fatalf("second click did not open the PR: screen=%v", m.screen)
	}
}

func TestPRListClickMapsThroughWindowOffset(t *testing.T) {
	m := listMouseModel(t, 20, 80, 10)
	m.prList.cursor = 10
	if cmd := m.sync(); cmd != nil {
		cmd()
	}
	offset := m.list.YOffset()
	if offset == 0 {
		t.Fatal("test needs a scrolled window")
	}
	// Land on the first full row start at or below the offset.
	line := offset + (3-offset%3)%3
	u, _ := m.Update(click(2, m.headerHeight()+1+line-offset))
	m = u.(Model)
	if want := line / 3; m.prList.cursor != want {
		t.Fatalf("click through offset %d selected %d, want %d", offset, m.prList.cursor, want)
	}
}

func TestPRListClickMapsThroughStackHeadersAndCollapse(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "other"
	m.navigator.PRs = []gh.PR{
		{Number: 3, Title: "UI", BaseRefName: "stack/api", HeadRefName: "stack/ui"},
		{Number: 2, Title: "API", BaseRefName: "stack/model", HeadRefName: "stack/api"},
		{Number: 1, Title: "Model", BaseRefName: "main", HeadRefName: "stack/model"},
	}
	m.applyPRFilters(1)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	contentTop := m.headerHeight() + 1

	// The stack header occupies line 0; entry 1 renders at lines 4-5.
	u, _ = m.Update(click(5, contentTop+4))
	m = u.(Model)
	if m.prList.cursor != 1 {
		t.Fatalf("click below the stack header selected %d, want 1", m.prList.cursor)
	}
	// A click on the header line itself is a miss.
	u, _ = m.Update(click(5, contentTop))
	m = u.(Model)
	if m.prList.cursor != 1 || m.screen != prListScreen {
		t.Fatalf("header click changed state: cursor=%d screen=%v", m.prList.cursor, m.screen)
	}
	// Collapsing shifts the remaining row directly under the header.
	u, _ = m.Update(keyPress("space"))
	m = u.(Model)
	if len(m.prList.open) != 1 {
		t.Fatalf("collapse failed: %#v", m.prList.open)
	}
	m.prList.cursor = 0
	u, _ = m.Update(click(5, contentTop+1))
	m = u.(Model)
	if m.screen != detailScreen {
		t.Fatalf("click on the collapsed stack's selected row did not open it: screen=%v", m.screen)
	}
}

func TestPRListTabClickSwitchesView(t *testing.T) {
	m := listMouseModel(t, 1, 120, 30)
	_, zones := m.prListTabLayout()
	var zone tabZone
	found := false
	for _, z := range zones {
		if z.view == reviewRequestedView {
			zone, found = z, true
			break
		}
	}
	if !found {
		t.Fatal("no zone for the review-requested tab")
	}
	x := zone.x0
	if m.headerTextWidth() != m.w {
		x += logoWidth
	}
	u, _ := m.Update(click(x+1, zone.row))
	m = u.(Model)
	if m.prList.view != reviewRequestedView {
		t.Fatalf("tab click switched to view %v, want %v", m.prList.view, reviewRequestedView)
	}
	// A header click outside every tab zone does nothing.
	u, _ = m.Update(click(0, 0))
	m = u.(Model)
	if m.prList.view != reviewRequestedView {
		t.Fatalf("heading click changed the view to %v", m.prList.view)
	}
}

func TestWheelOverEachListPaneScrollsTheRightThing(t *testing.T) {
	m := listMouseModel(t, 20, 120, 30)
	listPaneW := m.list.Width() + paneChromeW

	u, _ := m.Update(wheel(5, 10, true))
	m = u.(Model)
	if m.prList.cursor != 3 {
		t.Fatalf("wheel over the list moved cursor to %d, want 3", m.prList.cursor)
	}
	u, _ = m.Update(wheel(5, 10, false))
	m = u.(Model)
	if m.prList.cursor != 0 {
		t.Fatalf("wheel up over the list moved cursor to %d, want 0", m.prList.cursor)
	}

	m.detail.SetContent(strings.Repeat("line\n", 200))
	u, _ = m.Update(wheel(listPaneW+5, 10, true))
	m = u.(Model)
	if m.prList.cursor != 0 || m.detail.YOffset() == 0 {
		t.Fatalf("wheel over the preview: cursor=%d previewOffset=%d", m.prList.cursor, m.detail.YOffset())
	}
}

// detailMouseModel is a sized detail screen in file-explorer mode with a
// long conversation, two files, two commits, and a PR with two checks.
func detailMouseModel(t *testing.T) Model {
	t.Helper()
	m := testModel()
	m.screen = detailScreen
	for i := 0; i < 12; i++ {
		m.detailView.events = append(m.detailView.events, m.detailView.events[0])
	}
	m.detailView.files = []git.ChangedFile{
		{Status: "M", Path: "a.go", Fingerprint: "fa"},
		{Status: "M", Path: "b.go", Fingerprint: "fb"},
	}
	m.detailView.commits = []git.Commit{
		{SHA: "abc1234", Subject: "feat: x", Date: "2026-07-21T11:00"},
		{SHA: "def5678", Subject: "feat: y", Date: "2026-07-21T12:00"},
	}
	m.cache.PR = &gh.PR{Number: 9, State: "OPEN", Checks: []gh.PRCheck{{Name: "build"}, {Name: "test"}}}
	m.detailView.rawCache = map[string]string{}
	for _, file := range m.detailView.files {
		key := "file:" + m.detailView.diffBase + "..." + m.detailView.headRev + ":" + file.Status + "::" + file.Path
		m.detailView.rawCache[key] = strings.Repeat("diff line\n", 100)
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return u.(Model)
}

func TestDetailWheelRoutesByPane(t *testing.T) {
	m := detailMouseModel(t)
	if !m.fileExplorerMode() {
		t.Fatal("test expects file-explorer mode")
	}
	leftPaneW := m.list.Width() + paneChromeW

	// Conversation pane: the wheel scrolls the left viewport.
	u, _ := m.Update(wheel(5, 10, true))
	m = u.(Model)
	if m.list.YOffset() != wheelRows {
		t.Fatalf("wheel over conversation scrolled to %d, want %d", m.list.YOffset(), wheelRows)
	}
	// Explorer column: the wheel moves the file cursor.
	u, _ = m.Update(wheel(leftPaneW+3, 10, true))
	m = u.(Model)
	if m.detailView.fileCursor != 1 {
		t.Fatalf("wheel over explorer moved fileCursor to %d, want 1", m.detailView.fileCursor)
	}
	u, _ = m.Update(wheel(leftPaneW+3, 10, false))
	m = u.(Model)
	if m.detailView.fileCursor != 0 {
		t.Fatalf("wheel up over explorer moved fileCursor to %d, want 0", m.detailView.fileCursor)
	}
	// Diff region: the wheel scrolls the detail viewport.
	diffX := leftPaneW + 2 + m.explorer.Width() + dividerW
	u, _ = m.Update(wheel(diffX+2, 10, true))
	m = u.(Model)
	if m.detail.YOffset() != wheelRows {
		t.Fatalf("wheel over diff scrolled to %d, want %d", m.detail.YOffset(), wheelRows)
	}
}

func TestDetailClickSelectsConversationFilesCommitsAndChecks(t *testing.T) {
	m := detailMouseModel(t)
	contentTop := m.headerHeight() + 1
	leftPaneW := m.list.Width() + paneChromeW

	rows := m.detailView.conversationRows
	if len(rows) < 2 {
		t.Fatalf("conversation rows missing: %v", rows)
	}
	u, _ := m.Update(click(5, contentTop+rows[1][0]))
	m = u.(Model)
	if m.detailView.cursors[conversationTab] != 1 {
		t.Fatalf("conversation click selected %d, want 1", m.detailView.cursors[conversationTab])
	}
	// A separator line between items is a miss.
	u, _ = m.Update(click(5, contentTop+rows[1][1]))
	m = u.(Model)
	if m.detailView.cursors[conversationTab] != 1 {
		t.Fatalf("separator click moved the cursor to %d", m.detailView.cursors[conversationTab])
	}

	// File-explorer rows sit one line under the explorer title.
	u, _ = m.Update(click(leftPaneW+3, contentTop+2))
	m = u.(Model)
	if m.detailView.fileCursor != 1 || m.detailView.focus != focusExplorer {
		t.Fatalf("explorer click: fileCursor=%d focus=%v", m.detailView.fileCursor, m.detailView.focus)
	}

	// Clicking back into the conversation returns focus to the left pane.
	u, _ = m.Update(click(5, contentTop+rows[0][0]))
	m = u.(Model)
	if m.detailView.focus != focusConversation || m.detailView.cursors[conversationTab] != 0 {
		t.Fatalf("conversation click after explorer: focus=%v cursor=%d", m.detailView.focus, m.detailView.cursors[conversationTab])
	}

	// Commits tab includes a publication-section heading above its rows.
	u, _ = m.Update(keyPress("c"))
	m = u.(Model)
	u, _ = m.Update(click(5, contentTop+2))
	m = u.(Model)
	if m.detailView.cursors[commitsTab] != 1 || m.detailView.focus != focusConversation {
		t.Fatalf("commit click: cursor=%d focus=%v", m.detailView.cursors[commitsTab], m.detailView.focus)
	}

	// Checks tab renders a freshness header above the rows; the click math
	// must account for it, and a click on the header is a miss.
	u, _ = m.Update(keyPress("i"))
	m = u.(Model)
	if m.detailView.active != checksTab {
		t.Fatalf("checks tab not active: %v", m.detailView.active)
	}
	_, selectedLine := m.buildList()
	start := selectedLine - m.detailView.cursors[checksTab]
	if start == 0 {
		t.Fatal("test expects a header above the checks")
	}
	u, _ = m.Update(click(5, contentTop+start+1))
	m = u.(Model)
	if m.detailView.cursors[checksTab] != 1 {
		t.Fatalf("check click selected %d, want 1", m.detailView.cursors[checksTab])
	}
	u, _ = m.Update(click(5, contentTop))
	m = u.(Model)
	if m.detailView.cursors[checksTab] != 1 {
		t.Fatalf("header click moved the checks cursor to %d", m.detailView.cursors[checksTab])
	}
}

func TestOverlayAndActionPopupSwallowWheel(t *testing.T) {
	m := listMouseModel(t, 20, 120, 30)
	m.overlay = prStatusOverlay{pr: gh.PR{Number: 1, State: "OPEN"}}
	u, _ := m.Update(wheel(5, 10, true))
	m = u.(Model)
	if m.prList.cursor != 0 {
		t.Fatalf("wheel under the status popup moved cursor to %d", m.prList.cursor)
	}
	m.overlay = nil
	m.pendingPRAction, m.prActionPR, m.prActionNumber = closePR, m.prList.open[0], 1
	u, _ = m.Update(wheel(5, 10, true))
	m = u.(Model)
	if m.prList.cursor != 0 {
		t.Fatalf("wheel under the action popup moved cursor to %d", m.prList.cursor)
	}
}

func TestPRStatusPopupMouse(t *testing.T) {
	var gotTarget string
	m := listMouseModel(t, 1, 120, 30)
	m.client = &fakeGH{setStatus: func(pr gh.PR, target string) error {
		gotTarget = target
		return nil
	}}
	pr := gh.PR{Number: 7, State: "OPEN"}
	m.overlay = prStatusOverlay{pr: pr}

	popup := prStatusOverlay{pr: pr}.render(m)
	left, top, _, _ := m.popupRect(popup)
	row := popupOptionRow(popup, "Draft")
	if row < 0 {
		t.Fatalf("no Draft row in popup: %q", popup)
	}
	u, _ := m.Update(click(left+5, top+row))
	m = u.(Model)
	if o, ok := m.overlay.(prStatusOverlay); !ok || o.cursor != 1 {
		t.Fatalf("option click did not move the cursor: %#v", m.overlay)
	}
	u, cmd := m.Update(click(left+5, top+row))
	m = u.(Model)
	if o, ok := m.overlay.(prStatusOverlay); !ok || !o.running || cmd == nil {
		t.Fatalf("second click did not confirm: overlay=%#v cmd=%v", m.overlay, cmd)
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, inner := range batch {
			inner()
		}
	}
	if gotTarget != "draft" {
		t.Fatalf("confirmed target = %q, want draft", gotTarget)
	}

	// A click outside the popup cancels it.
	m.overlay = prStatusOverlay{pr: pr}
	u, _ = m.Update(click(0, 0))
	m = u.(Model)
	if m.overlay != nil {
		t.Fatalf("outside click left the popup open: %#v", m.overlay)
	}
}

func TestMergePopupMouse(t *testing.T) {
	var gotMethod gh.MergeMethod
	calls := 0
	m := listMouseModel(t, 1, 120, 30)
	m.client = &fakeGH{merge: func(number int, headOID string, method gh.MergeMethod) error {
		gotMethod, calls = method, calls+1
		return nil
	}}

	u, _ := m.Update(keyPress("m"))
	m = u.(Model)
	if m.pendingPRAction != mergePR {
		t.Fatalf("merge popup not open: %v", m.pendingPRAction)
	}
	popup := m.renderActionPopup()
	left, top, _, _ := m.popupRect(popup)
	row := popupOptionRow(popup, mergeMethodLabel(gh.MergeSquash))
	if row < 0 {
		t.Fatalf("no Squash row in popup: %q", popup)
	}
	u, _ = m.Update(click(left+5, top+row))
	m = u.(Model)
	if m.mergeMethodCursor != 1 || m.prActionRunning != noPRAction {
		t.Fatalf("option click: cursor=%d running=%v", m.mergeMethodCursor, m.prActionRunning)
	}
	u, cmd := m.Update(click(left+5, top+row))
	m = u.(Model)
	if m.prActionRunning != mergePR || cmd == nil {
		t.Fatalf("second click did not confirm the merge: running=%v", m.prActionRunning)
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, inner := range batch {
			inner()
		}
	}
	if gotMethod != gh.MergeSquash || calls != 1 {
		t.Fatalf("merge call = %q ×%d, want one squash", gotMethod, calls)
	}

	// Outside click cancels a reopened popup without touching the client.
	m.prActionRunning = noPRAction
	u, _ = m.Update(keyPress("m"))
	m = u.(Model)
	u, _ = m.Update(click(0, 0))
	m = u.(Model)
	if m.pendingPRAction != noPRAction || calls != 1 {
		t.Fatalf("outside click: pending=%v calls=%d", m.pendingPRAction, calls)
	}

	// Confirm-only popups never confirm from clicks: a click inside the
	// close popup does nothing, a click outside cancels.
	u, _ = m.Update(keyPress("x"))
	m = u.(Model)
	if m.pendingPRAction != closePR {
		t.Fatalf("close popup not open: %v", m.pendingPRAction)
	}
	popup = m.renderActionPopup()
	left, top, _, _ = m.popupRect(popup)
	u, _ = m.Update(click(left+3, top+2))
	m = u.(Model)
	if m.pendingPRAction != closePR || m.prActionRunning != noPRAction {
		t.Fatalf("inside click acted on the close popup: pending=%v running=%v", m.pendingPRAction, m.prActionRunning)
	}
	u, _ = m.Update(click(0, 0))
	m = u.(Model)
	if m.pendingPRAction != noPRAction {
		t.Fatalf("outside click did not cancel the close popup: %v", m.pendingPRAction)
	}
}

func TestEditorOverlayIgnoresOutsideClicks(t *testing.T) {
	m := listMouseModel(t, 1, 120, 30)
	m.overlay = localEditOverlay{}
	u, _ := m.Update(click(0, 0))
	m = u.(Model)
	if _, ok := m.overlay.(localEditOverlay); !ok {
		t.Fatalf("outside click closed the editor overlay: %#v", m.overlay)
	}
	u, _ = m.Update(wheel(5, 10, true))
	m = u.(Model)
	if m.prList.cursor != 0 {
		t.Fatalf("wheel under the editor overlay moved cursor to %d", m.prList.cursor)
	}
}
