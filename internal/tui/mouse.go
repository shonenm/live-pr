// Mouse routing for both screens and the modal popups: wheel scrolling is
// pane-aware by pointer position, clicks select (and, on the selected PR row,
// open), header tabs switch views, and popups handle their own clicks while
// swallowing everything else.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/internal/github"
)

// wheelRows is how far one wheel tick moves: three PR-list selection steps
// (like j/k ×3) or three viewport lines.
const wheelRows = 3

func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if !m.ready {
		return m, nil
	}
	// The PR-action popup is modal like an overlay: while one runs the mouse
	// is dead, and while one is pending only its own clicks act.
	if m.prActionRunning != noPRAction {
		return m, nil
	}
	if m.pendingPRAction != noPRAction {
		return m.handleActionPopupMouse(msg)
	}
	if m.screen == prListScreen {
		return m.handlePRListMouse(msg)
	}
	return m.handleDetailMouse(msg)
}

// wheelDelta maps a wheel message to a scroll direction: -1 up, +1 down,
// 0 for horizontal wheels, which nothing here consumes.
func wheelDelta(msg tea.MouseWheelMsg) int {
	switch msg.Button {
	case tea.MouseWheelUp:
		return -1
	case tea.MouseWheelDown:
		return 1
	}
	return 0
}

func (m Model) handlePRListMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	mouse := msg.Mouse()
	listPaneW := m.list.Width() + paneChromeW
	contentTop := m.headerHeight() + 1 // header rows plus the pane's top border
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		delta := wheelDelta(msg)
		if delta == 0 || mouse.Y < m.headerHeight() || mouse.Y >= m.h-footerLines {
			return m, nil
		}
		if mouse.X < listPaneW {
			// Wheel over the list moves the selection like j/k, so the
			// windowed render, keepLineVisible, and last-row pagination all
			// keep working.
			return m, m.moveCursorBy(wheelRows * delta)
		}
		scrollQuarter(&m.detail, delta > 0)
		return m, nil
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		if view, ok := m.viewTabAt(mouse.X, mouse.Y); ok {
			selected := m.prList.selectedPRNumber()
			m.prList.view = view
			return m, m.applyPRViewState(selected)
		}
		if mouse.X >= listPaneW {
			return m, nil
		}
		line := mouse.Y - contentTop
		if line < 0 || line >= m.list.Height() {
			return m, nil
		}
		index := m.prIndexAtListLine(line + m.list.YOffset())
		if index < 0 {
			return m, nil
		}
		if index == m.prList.cursor {
			return m.openSelectedPR()
		}
		return m, m.moveCursorTo(index)
	}
	return m, nil
}

// viewTabAt resolves the view tab under a header click, if any. The zones
// come from the same layout pass that rendered the tab rows.
func (m Model) viewTabAt(x, y int) (prView, bool) {
	rows, zones := m.prListTabLayout()
	if y >= len(rows) {
		return 0, false
	}
	if m.headerTextWidth() != m.w {
		x -= logoWidth // the wordmark shifts the header text block right
	}
	for _, zone := range zones {
		if zone.row == y && x >= zone.x0 && x < zone.x1 {
			return zone.view, true
		}
	}
	return 0, false
}

func (m Model) handleDetailMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Embedded reviewer forwarding stays first, exactly as before: events
	// inside the review content translate to local coordinates and focus it.
	// The review pane sits after the bordered left pane; +1 row for its own
	// top border.
	if m.diffTerminal != nil && m.diffTerminal.Available() {
		if local, ok := translateDiffMouse(msg, m.list.Width()+paneChromeW, m.detail.Width(), m.detail.Height(), m.headerHeight()+1); ok {
			m.detailView.focus = focusReview
			return m, m.diffTerminal.Update(local)
		}
	}
	// Wide mode shows only the focused side, so the other pane has no
	// clickable region at all.
	conversationVisible := !m.detailView.reviewWide || m.detailView.focus == focusConversation
	reviewVisible := !m.detailView.reviewWide || m.detailView.focus != focusConversation
	leftPaneW := 0
	if conversationVisible {
		leftPaneW = m.list.Width() + paneChromeW
		if !reviewVisible {
			leftPaneW = m.w
		}
	}
	mouse := msg.Mouse()
	contentTop := m.headerHeight() + 1
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		delta := wheelDelta(msg)
		if delta == 0 || mouse.Y < m.headerHeight() || mouse.Y >= m.h-footerLines {
			return m, nil
		}
		if conversationVisible && mouse.X < leftPaneW {
			if delta < 0 {
				m.list.ScrollUp(wheelRows)
			} else {
				m.list.ScrollDown(wheelRows)
			}
			return m, nil
		}
		if !reviewVisible {
			return m, nil
		}
		if m.fileExplorerMode() && m.mouseInExplorer(mouse.X, leftPaneW) {
			// The explorer is re-anchored to its cursor on every sync, so
			// the wheel moves the selection rather than fighting it.
			next := min(max(m.detailView.fileCursor+delta, 0), len(m.detailView.files)-1)
			if next >= 0 && next != m.detailView.fileCursor {
				m.detailView.fileCursor = next
				return m, m.sync()
			}
			return m, nil
		}
		// Static diff / raw view; with the embedded reviewer running its
		// content region already consumed the wheel above.
		if delta < 0 {
			m.detail.ScrollUp(wheelRows)
		} else {
			m.detail.ScrollDown(wheelRows)
		}
		return m, nil
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		if conversationVisible && mouse.X < leftPaneW {
			line := mouse.Y - contentTop
			if line < 0 || line >= m.list.Height() {
				return m, nil
			}
			index := m.detailListIndexAtLine(line + m.list.YOffset())
			if m.detailView.focus != focusConversation {
				m.detailView.focus = focusConversation
				m.layout()
				if index < 0 {
					return m, m.sync()
				}
			}
			if index < 0 {
				return m, nil
			}
			return m, m.moveCursorTo(index)
		}
		if !reviewVisible || mouse.Y < m.headerHeight() || mouse.Y >= m.h-footerLines {
			return m, nil
		}
		if m.fileExplorerMode() && m.mouseInExplorer(mouse.X, leftPaneW) {
			line := mouse.Y - contentTop + m.explorer.YOffset()
			_, selectedFileLine := m.buildFileExplorer()
			index := line - (selectedFileLine - m.detailView.fileCursor)
			if index < 0 || index >= len(m.detailView.files) {
				return m, nil
			}
			if m.detailView.focus != focusExplorer {
				m.detailView.focus = focusExplorer
				m.layout()
			}
			return m, m.moveCursorTo(index)
		}
		// Click-to-focus on the review side (static diff or the reviewer's
		// border cells).
		if m.detailView.focus == focusConversation {
			m.detailView.focus = focusReview
			m.layout()
			return m, m.sync()
		}
		return m, nil
	}
	return m, nil
}

// mouseInExplorer reports whether x lands on the file-explorer column of the
// review pane, which starts after the pane border and padding.
func (m Model) mouseInExplorer(x, leftPaneW int) bool {
	start := leftPaneW + 2
	return x >= start && x < start+m.explorer.Width()
}

// detailListIndexAtLine maps a left-pane content line to the row index of
// the active tab. The first-row offset is derived from the same build that
// filled the viewport (its selected line minus the cursor), so headers above
// the rows are accounted for without duplicating their layout.
func (m *Model) detailListIndexAtLine(line int) int {
	if m.detailView.active == conversationTab {
		return m.conversationIndexAtLine(line)
	}
	if m.detailView.active == commitsTab {
		published := m.publishedCommits
		if m.remote {
			published = len(m.detailView.commits)
		}
		published = min(published, len(m.detailView.commits))
		row := 0
		for i := range m.detailView.commits {
			if i == 0 && published > 0 {
				row++
			}
			if i == published && published < len(m.detailView.commits) {
				if row > 0 {
					row++
				}
				row++
			}
			if line == row {
				return i
			}
			row++
		}
		if len(m.detailView.remoteCommits) > 0 {
			if row > 0 {
				row++
			}
			row++ // heading
			for j := range m.detailView.remoteCommits {
				if line == row {
					return len(m.detailView.commits) + j
				}
				row++
			}
		}
		if !m.remote && m.worktreeSummary.Total() > 0 {
			if row > 0 {
				row++
			}
			row++ // heading
			if line == row {
				return len(m.detailView.commits) + len(m.detailView.remoteCommits)
			}
		}
		return -1
	}
	_, selectedLine := m.buildList()
	index := line - (selectedLine - m.detailView.cursors[m.detailView.active])
	if index < 0 || index >= m.activeLen() {
		return -1
	}
	return index
}

// popupRect returns the screen rectangle overlayPopup gives popup: the same
// centering origin, the popup's width, and its line count.
func (m Model) popupRect(popup string) (left, top, width, height int) {
	left, top = overlayOrigin(m.baseContent(), popup, m.w)
	return left, top, lipgloss.Width(popup), strings.Count(popup, "\n") + 1
}

func (m Model) mouseInPopup(popup string, mouse tea.Mouse) bool {
	left, top, width, height := m.popupRect(popup)
	return mouse.X >= left && mouse.X < left+width && mouse.Y >= top && mouse.Y < top+height
}

// popupOptionRow finds the popup line rendering the given option label,
// matching the stripped row text with or without the cursor marker. Working
// from the rendered popup keeps the hit-test true to what is on screen even
// when other popup lines wrap.
func popupOptionRow(popup, label string) int {
	for i, row := range strings.Split(popup, "\n") {
		text := strings.Trim(ansi.Strip(row), "│ \t") // border cells and padding
		if strings.TrimPrefix(text, "▸ ") == label {
			return i
		}
	}
	return -1
}

// leftClick unwraps a left-button click; every other mouse event yields ok
// false, which popup handlers treat as "swallow".
func leftClick(msg tea.MouseMsg) (tea.Mouse, bool) {
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Button != tea.MouseLeft {
		return tea.Mouse{}, false
	}
	return click.Mouse(), true
}

// handleActionPopupMouse drives the pending PR-action popup: the merge
// picker's options move and confirm on clicks, and a click outside any
// pending popup cancels it — the Esc equivalent. Confirm-only popups
// (checkout, close) keep confirmation on y/n so a destructive action never
// fires from a stray click.
func (m Model) handleActionPopupMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	mouse, ok := leftClick(msg)
	if !ok {
		return m, nil
	}
	popup := m.renderActionPopup()
	if !m.mouseInPopup(popup, mouse) {
		m.pendingPRAction, m.prActionNumber, m.prActionPR = noPRAction, 0, gh.PR{}
		return m, nil
	}
	if m.pendingPRAction != mergePR {
		return m, nil
	}
	_, top, _, _ := m.popupRect(popup)
	for i, method := range m.mergeMethodOptions() {
		if popupOptionRow(popup, mergeMethodLabel(method)) != mouse.Y-top {
			continue
		}
		if i == m.mergeMethodCursor {
			next, cmd, _ := m.submitMerge(method)
			return next, cmd
		}
		m.mergeMethodCursor = i
		return m, nil
	}
	return m, nil
}

// dismissOnOutsideClick closes a confirm popup when a click lands outside
// it; clicks inside and every other mouse event are swallowed, keeping the
// confirmation itself on the keyboard.
func dismissOnOutsideClick(m Model, o overlay, msg tea.MouseMsg) (Model, tea.Cmd) {
	mouse, ok := leftClick(msg)
	if !ok || m.mouseInPopup(o.render(m), mouse) {
		return m, nil
	}
	m.overlay = nil
	return m, nil
}

func (o localDeleteOverlay) handleMouse(m Model, msg tea.MouseMsg) (Model, tea.Cmd) {
	return dismissOnOutsideClick(m, o, msg)
}

func (o remoteDeleteOverlay) handleMouse(m Model, msg tea.MouseMsg) (Model, tea.Cmd) {
	return dismissOnOutsideClick(m, o, msg)
}

func (o outboxDiscardOverlay) handleMouse(m Model, msg tea.MouseMsg) (Model, tea.Cmd) {
	return dismissOnOutsideClick(m, o, msg)
}

// handleMouse gives the status picker the option-list contract: a click on
// an option moves the cursor, a click on the selected option confirms it,
// and a click outside cancels the popup.
func (o prStatusOverlay) handleMouse(m Model, msg tea.MouseMsg) (Model, tea.Cmd) {
	if o.running {
		return m, nil
	}
	mouse, ok := leftClick(msg)
	if !ok {
		return m, nil
	}
	popup := o.render(m)
	if !m.mouseInPopup(popup, mouse) {
		m.overlay = nil
		return m, nil
	}
	_, top, _, _ := m.popupRect(popup)
	for i, option := range availablePRStatusOptions(o.pr) {
		if popupOptionRow(popup, prStatusActionLabel(option)) != mouse.Y-top {
			continue
		}
		if i == o.cursor {
			return o.submitTarget(m, option, nil)
		}
		o.cursor = i
		m.overlay = o
		return m, nil
	}
	return m, nil
}
