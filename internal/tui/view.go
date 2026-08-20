// Rendering of the header, panes, footer, and shared row helpers.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/git"
)

func (m Model) busyStatus(text string) string {
	if text == "" {
		return m.loadSpinner.View()
	}
	return m.loadSpinner.View() + " " + renderStatus(text)
}

// baseBranchStyle marks a merge target that is the repository's default
// branch, so a PR stacked on another branch stands out at a glance.
func (m Model) baseBranchStyle(ref string) lipgloss.Style {
	if m.isDefaultBranch(ref) {
		return stAccent.Bold(true)
	}
	return stBold
}

// View assembles the frame and declares terminal features: the alt screen
// and cell-motion mouse mode that v1 passed as program options, plus the real
// terminal cursor. When a text input is focused the cursor sits on its caret
// cell so IME preedit text composes in place; otherwise it stays hidden.
func (m Model) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	base := m.baseContent()
	popup, hasPopup := m.popupContent()
	if !hasPopup {
		view.SetContent(base)
		view.Cursor = m.filterCursor()
		return view
	}
	view.SetContent(overlayPopup(base, popup, m.w))
	view.Cursor = m.overlayCursor(base, popup)
	return view
}

// viewContent renders the whole frame as a string.
func (m Model) viewContent() string {
	base := m.baseContent()
	if popup, ok := m.popupContent(); ok {
		return overlayPopup(base, popup, m.w)
	}
	return base
}

// popupContent renders the open modal popup, if any.
func (m Model) popupContent() (string, bool) {
	if m.overlay != nil {
		return m.overlay.render(m), true
	}
	if m.pendingPRAction != noPRAction || m.prActionRunning != noPRAction {
		return m.renderActionPopup(), true
	}
	return "", false
}

// overlayCursor translates an input-hosting popup's caret to screen
// coordinates by adding the same centering offset overlayPopup uses.
func (m Model) overlayCursor(base, popup string) *tea.Cursor {
	host, ok := m.overlay.(cursorOverlay)
	if !ok {
		return nil
	}
	c := host.cursor(m)
	if c == nil {
		return nil
	}
	left, top := overlayOrigin(base, popup, m.w)
	c.X += left
	c.Y += top
	return c
}

// baseContent renders the current screen without any popup.
func (m Model) baseContent() string {
	if !m.ready {
		return "loading…"
	}
	var view string
	if m.screen == prListScreen {
		listTitle := fmt.Sprintf("%s · %d", m.viewName(m.prList.view), len(m.prList.filtered))
		previewTitle := "Preview"
		if pr := m.prList.selectedPR(); pr != nil {
			if pr.Number > 0 {
				previewTitle = fmt.Sprintf("Preview · #%d", pr.Number)
			} else {
				previewTitle = "Preview · local"
			}
		}
		listPane := renderPane(listTitle, m.list.View(), m.list.Width()+paneChromeW, m.list.Height()+paneChromeH, true)
		previewPane := renderPane(previewTitle, m.detail.View(), m.detail.Width()+paneChromeW, m.detail.Height()+paneChromeH, false)
		body := lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane)
		view = lipgloss.JoinVertical(lipgloss.Left, m.renderPRListHeader(), body, m.renderFooter())
	} else {
		leftTitle := "Conversation"
		switch m.detailView.active {
		case commitsTab:
			leftTitle = fmt.Sprintf("Commits · %d", len(m.detailView.commits))
		case conflictsTab:
			leftTitle = fmt.Sprintf("Conflicts · %d", len(m.detailView.mergeReadiness.ConflictFiles))
		case checksTab:
			count := 0
			if m.cache.PR != nil {
				count = len(m.cache.PR.Checks)
			}
			leftTitle = fmt.Sprintf("Checks · %d", count)
		}
		// Wide mode shows only the focused side. Explorer focus lives inside
		// the review pane, so it counts as the review side — matching
		// layout's conversationWide test keeps the sizes and the render in
		// step when Tab or l moves focus while wide.
		if m.detailView.reviewWide && m.detailView.focus != focusConversation {
			body := m.renderReviewPane()
			view = lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
		} else if m.detailView.reviewWide {
			leftContent := m.list.View()
			if m.detailView.active == conversationTab {
				leftContent = lipgloss.JoinVertical(lipgloss.Left, leftContent, m.conversationCounts())
			}
			height := max(3, m.h-m.headerHeight()-footerLines)
			left := renderPane(leftTitle, leftContent, m.w, height, true)
			view = lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), left, m.renderFooter())
		} else {
			leftContent := m.list.View()
			if m.detailView.active == conversationTab {
				leftContent = lipgloss.JoinVertical(lipgloss.Left, leftContent, m.conversationCounts())
			}
			left := renderPane(leftTitle, leftContent, m.list.Width()+paneChromeW, m.detail.Height()+paneChromeH, m.detailView.focus == focusConversation)
			body := lipgloss.JoinHorizontal(lipgloss.Top, left, m.renderReviewPane())
			view = lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
		}
	}
	return view
}

// headerHeight is the wordmark's height on both screens: the header text is
// never taller, so the metadata row costs no extra space.
func (m Model) headerHeight() int { return logoHeight }

// headerTextWidth is the width available to header text: the full terminal
// minus the wordmark when it is shown. withLogo and the filter-cursor math
// share this rule so the caret lands where the header actually drew the text.
func (m Model) headerTextWidth() int {
	if m.w >= logoWidth+40 {
		return m.w - logoWidth
	}
	return m.w
}

// withLogo anchors the wordmark at the far left of a header block and pads the
// text to the wordmark's height. Narrow terminals drop the wordmark but keep
// the height, so the layout never jumps.
func (m Model) withLogo(text string) string {
	width := m.headerTextWidth()
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(1, width), "…")
	}
	for len(lines) < logoHeight {
		lines = append(lines, "")
	}
	block := strings.Join(lines, "\n")
	if width != m.w {
		block = lipgloss.JoinHorizontal(lipgloss.Top, renderLogo(), block)
	}
	return m.withVersion(block)
}

func (m Model) versionLabel() string {
	if m.version == "" {
		return ""
	}
	label := m.version
	if label != "dev" && !strings.HasPrefix(label, "v") {
		label = "v" + label
	}
	return label
}

func (m Model) withVersion(block string) string {
	label := m.versionLabel()
	if label == "" || m.w <= 0 {
		return block
	}
	version := stMuted.Render(label)
	vw := lipgloss.Width(version)
	lines := strings.Split(block, "\n")
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	} else {
		lines = []string{""}
	}
	available := max(1, m.w-vw)
	first = ansi.Truncate(first, available, "…")
	if pad := m.w - lipgloss.Width(first) - vw; pad > 0 {
		first += strings.Repeat(" ", pad)
	}
	lines[0] = first + version
	return strings.Join(lines, "\n")
}

func (m Model) detailStats() git.ChangeStats {
	if !m.remote {
		stats := m.localStats
		if stats.Files == 0 && len(m.detailView.files) > 0 {
			stats.Files = len(m.detailView.files)
		}
		return stats
	}
	if m.cache.PR != nil {
		return git.ChangeStats{Files: m.cache.PR.ChangedFiles, Additions: m.cache.PR.Additions, Deletions: m.cache.PR.Deletions}
	}
	return git.ChangeStats{Files: len(m.detailView.files)}
}

func (m Model) renderHeader() string {
	badgeText, badgeColor := "Local", cBorder
	if m.cache.PR != nil {
		state := strings.ToLower(m.cache.PR.State)
		if m.cache.PR.IsDraft {
			state = "draft"
		}
		badgeText, badgeColor = fmt.Sprintf("⇄ #%d %s", m.cache.PR.Number, state), prStateBadgeColor(state)
	}
	badge := lipgloss.NewStyle().
		Background(lipgloss.Color(badgeColor)).Foreground(lipgloss.Color(emphasisInk(badgeColor))).
		Padding(0, 1).Render(badgeText)
	title := m.detailView.title
	if m.cache.PR != nil {
		title = m.cache.PR.Title
	}
	l1 := badge + "  " + stBold.Render(title)
	stats := m.detailStats()
	scope := fmt.Sprintf("%d files", stats.Files) + " " + stGreenF.Render(fmt.Sprintf("+%d", stats.Additions)) + " " + stRedF.Render(fmt.Sprintf("-%d", stats.Deletions))
	if m.detailView.reviewSHA != "" {
		scope = "commit " + m.detailView.reviewSHA
	}
	dirty := ""
	if !m.remote && m.workingTreeDirty {
		dirty = "   " + stAttention.Render("● uncommitted changes")
	}
	draft := ""
	if len(m.reviewDraft.Comments) > 0 || strings.TrimSpace(m.reviewDraft.Body) != "" {
		label := "✎ review draft"
		if n := len(m.reviewDraft.Comments); n > 0 {
			label += fmt.Sprintf(" · %d comments", n)
		}
		draft = "   " + stAttention.Render(label)
	}
	readiness := ""
	if m.cache.PR != nil || !m.remote {
		readiness = stMuted.Render(fmt.Sprintf("   · %d behind", m.detailView.mergeReadiness.Behind))
		if conflicts := len(m.detailView.mergeReadiness.ConflictFiles); conflicts > 0 {
			readiness += "   " + stRedF.Render(fmt.Sprintf("⚠ %d conflict files", conflicts))
		} else if m.detailView.mergeReadinessErr == nil {
			readiness += "   " + stGreenF.Render("✓ no conflicts")
		} else {
			readiness += "   " + stMuted.Render("merge readiness unavailable")
		}
	}
	closes := ""
	if m.cache.PR != nil && len(m.cache.PR.ClosingIssues) > 0 {
		refs := make([]string, 0, len(m.cache.PR.ClosingIssues))
		for _, issue := range m.cache.PR.ClosingIssues {
			refs = append(refs, fmt.Sprintf("#%d", issue.Number))
		}
		closes = "   " + stMuted.Render("⊙ closes "+strings.Join(refs, ", "))
	}
	l2 := stMuted.Render("⎇ ") + m.baseBranchStyle(m.detailView.base).Render(m.detailView.base) + stMuted.Render(" ← ") + stFg.Render(m.detailView.head) + stMuted.Render("   · ") + scope + dirty + draft + readiness + closes
	lines := []string{l1, l2}
	if m.cache.PR != nil {
		lines = append(lines, m.renderPRMeta(*m.cache.PR))
	}
	return m.withLogo(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m Model) footerMode() string {
	if m.screen == prListScreen {
		return "PRS"
	}
	switch {
	case m.detailView.focus != focusConversation:
		return "REVIEW"
	case m.detailView.active == commitsTab:
		return "COMMITS"
	case m.detailView.active == conflictsTab:
		return "CONFLICTS"
	case m.detailView.active == checksTab:
		return "CHECKS"
	default:
		return "CONV"
	}
}

func (m Model) renderFooter() string {
	return footerSegment(m.footerMode()) + " " + m.footerContent()
}

func (m Model) footerContent() string {
	if m.status != "" {
		if m.isLoading() {
			return m.busyStatus(m.status)
		}
		return renderStatus(m.status)
	}
	if m.pendingPRAction != noPRAction {
		return m.help.View(m.keys)
	}
	if m.prActionRunning != noPRAction {
		return m.busyStatus("")
	}
	// While work is in flight the progress line wins over a lingering
	// notice, so a reload after "Checked out PR #N" is visibly running.
	if m.notice != "" && !m.isLoading() {
		return stGreenF.Render(m.notice) + "  " + m.help.View(m.keys)
	}
	if m.detailView.focus == focusReview {
		hint := stMuted.Render("Review focused · Tab conversation · Shift+Tab full width · q quit")
		if m.githubStatus != "" {
			return stMuted.Render(m.githubStatus) + "  " + hint
		}
		return hint
	}
	if m.githubStatus != "" {
		if m.isLoading() {
			return m.busyStatus(m.githubStatus) + "  " + m.help.View(m.keys)
		}
		return stMuted.Render(m.githubStatus) + "  " + m.help.View(m.keys)
	}
	return m.help.View(m.keys)
}

func renderStatus(status string) string {
	for _, prefix := range []string{"loading ", "publishing ", "wait ", "select ", "local git data"} {
		if strings.HasPrefix(status, prefix) {
			return stAttention.Render(status)
		}
	}
	return stRedF.Render(status)
}

func selectionBar(selected bool) string {
	if selected {
		return stAccent.Render("▌") + " "
	}
	return "  "
}

// selectionBgOpen returns the opening SGR that paints the selected background,
// or "" when the renderer has color disabled.
func selectionBgOpen() string {
	s := lipgloss.NewStyle().Background(lipgloss.Color(cSelectedBg)).Render("\x00")
	open, _, _ := strings.Cut(s, "\x00")
	return open
}

// highlightSelectedBg paints a full-width selected background across a line,
// re-applying the background after each reset so pre-styled segments keep it.
func highlightSelectedBg(line string, width int) string {
	open := selectionBgOpen()
	// Lip Gloss v2 always emits SGR codes (the renderer downsamples), so a
	// zero width no longer implies "no color, return as-is"; skip explicitly
	// before layout has given the pane a real width.
	if open == "" || width <= 0 {
		return line
	}
	line = ansi.Truncate(line, width, "…")
	if gap := width - lipgloss.Width(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return open + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+open) + "\x1b[0m"
}

func shortTS(ts string) string { return strings.Replace(ts, "T", " ", 1) }

// relativeTS renders ts relative to now, gh style: "just now", "5m ago",
// "3h ago", "12d ago", then "Jan 2" ("Jan 2, 2006" across years).
// Unparseable input falls back to shortTS.
func relativeTS(now time.Time, ts string) string {
	t := conversationTime(ts)
	if t.IsZero() {
		return shortTS(ts)
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	default:
		return t.Format("Jan 2, 2006")
	}
}
