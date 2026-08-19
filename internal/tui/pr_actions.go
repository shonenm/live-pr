package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prfilter"
)

// mergeMethodOptions lists the picker choices; the merge commit stays first
// so the default selection keeps the historical behaviour.
var mergeMethodOptions = []gh.MergeMethod{gh.MergeCommit, gh.MergeSquash, gh.MergeRebase}

func mergeMethodLabel(method gh.MergeMethod) string {
	switch method {
	case gh.MergeSquash:
		return "Squash"
	case gh.MergeRebase:
		return "Rebase"
	default:
		return "Merge commit"
	}
}

func mergeMethodPrompt(method gh.MergeMethod) string {
	switch method {
	case gh.MergeSquash:
		return "Squash and merge?"
	case gh.MergeRebase:
		return "Rebase and merge?"
	default:
		return "Merge with a merge commit?"
	}
}

// selectedMergeMethod maps the popup cursor to a method, defaulting to the
// merge commit when the cursor is out of range.
func (m Model) selectedMergeMethod() gh.MergeMethod {
	if m.mergeMethodCursor < 0 || m.mergeMethodCursor >= len(mergeMethodOptions) {
		return gh.MergeCommit
	}
	return mergeMethodOptions[m.mergeMethodCursor]
}

func runPRAction(client githubClient, action prAction, pr gh.PR, method gh.MergeMethod) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch action {
		case mergePR:
			err = client.Merge(pr.Number, pr.HeadRefOID, method)
		case checkoutPR:
			err = client.Checkout(pr.Number)
		case closePR:
			err = client.Close(pr.Number)
		}
		return prActionDone{action: action, pr: pr, number: pr.Number, err: err}
	}
}

func actionLabel(action prAction) string {
	switch action {
	case mergePR:
		return "Merge"
	case checkoutPR:
		return "Checkout"
	case closePR:
		return "Close"
	default:
		return "Action"
	}
}

// mergeConditionLines summarizes the merge conditions the confirmation is
// about to act on: draft state, mergeability, the CI rollup, the review
// decision when GitHub reported one, and — when the popup targets the PR
// loaded on the detail screen — local base freshness and conflict files.
// Display-only: every value is already in memory, nothing is fetched here.
func (m Model) mergeConditionLines(pr gh.PR) []string {
	var lines []string
	if pr.IsDraft {
		lines = append(lines, stAttention.Render("◌ draft"))
	}
	if text, style := mergeState(pr); text != "" {
		lines = append(lines, style.Render(text))
	}
	ciText, ciStyle := prCheckState(pr)
	ci := ciStyle.Render(ciText)
	if pending, failed, _ := prfilter.CheckCounts(pr.Checks); failed > 0 && pending > 0 {
		// CheckHealth surfaces only the failures; keep the still-running
		// count visible so a red rollup is not mistaken for a finished one.
		ci += stMuted.Render(fmt.Sprintf(" · %d pending", pending))
	}
	lines = append(lines, ci)
	if pr.ReviewDecision != "" {
		lines = append(lines, reviewSummary(pr.ReviewDecision))
	}
	// Behind/conflict scans are computed for the detail target only, so show
	// them just when the popup is about that same PR.
	if m.screen == detailScreen && m.cache.PR != nil && m.cache.PR.Number == pr.Number {
		readiness := m.detailView.mergeReadiness
		if n := len(readiness.ConflictFiles); n > 0 {
			lines = append(lines, stRedF.Render(fmt.Sprintf("⚠ %d conflict file%s", n, plural(n))))
		}
		switch {
		case readiness.Behind > 0:
			lines = append(lines, stAttention.Render(fmt.Sprintf("⚠ %d commit%s behind base", readiness.Behind, plural(readiness.Behind))))
		case m.detailView.mergeReadinessErr == nil && len(readiness.ConflictFiles) == 0:
			lines = append(lines, stGreenF.Render("✓ up to date with base"))
		}
	}
	return lines
}

func (m Model) renderActionPopup() string {
	action := m.prActionRunning
	if action == noPRAction {
		action = m.pendingPRAction
	}
	label := actionLabel(action)
	message := "Continue?"
	var options []string
	switch action {
	case mergePR:
		message = mergeMethodPrompt(m.selectedMergeMethod())
		for i, method := range mergeMethodOptions {
			prefix, style := "  ", stFg
			if i == m.mergeMethodCursor {
				prefix, style = "▸ ", stAccent.Bold(true)
			}
			options = append(options, prefix+style.Render(mergeMethodLabel(method)))
		}
	case checkoutPR:
		branch := m.prActionPR.HeadRefName
		if branch == "" {
			branch = "its branch"
		}
		message = "Checkout " + branch + "?"
	case closePR:
		message = "Close without merging?"
	}
	lines := []string{stBold.Render(fmt.Sprintf("%s PR #%d", label, m.prActionNumber)), ""}
	if action == mergePR {
		if conditions := m.mergeConditionLines(m.prActionPR); len(conditions) > 0 {
			lines = append(lines, conditions...)
			lines = append(lines, "")
		}
	}
	if len(options) > 0 && m.prActionRunning == noPRAction {
		lines = append(lines, options...)
		lines = append(lines, "")
	}
	lines = append(lines, stFg.Render(message))
	if m.prActionRunning != noPRAction {
		lines = append(lines, "", stAttention.Render(m.loadSpinner.View()+" "+label+" in progress…"))
	} else if action == mergePR {
		lines = append(lines, "", stMuted.Render("j/k select · y confirm · m/s/r · Esc cancel"))
	} else {
		lines = append(lines, "", stMuted.Render("y confirm · n / Esc cancel"))
	}
	contentWidth := max(1, min(52, m.w-14))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAccent)).
		Padding(1, 3).
		Width(contentWidth).
		Render(strings.Join(lines, "\n"))
}

func overlayPopup(base, popup string, width int) string {
	if width <= 0 {
		width = lipgloss.Width(base)
	}
	baseLines := strings.Split(base, "\n")
	popupLines := strings.Split(popup, "\n")
	popupWidth := lipgloss.Width(popup)
	if popupWidth == 0 || len(baseLines) == 0 {
		return base
	}
	left := max(0, (width-popupWidth)/2)
	top := max(0, (len(baseLines)-len(popupLines))/2)
	for i, popupLine := range popupLines {
		lineIndex := top + i
		if lineIndex >= len(baseLines) {
			break
		}
		line := ansi.Truncate(baseLines[lineIndex], width, "")
		if lineWidth := lipgloss.Width(line); lineWidth < width {
			line += strings.Repeat(" ", width-lineWidth)
		}
		cutRight := min(width, left+popupWidth)
		merged := ansi.Cut(line, 0, left) + popupLine + ansi.Cut(line, cutRight, width)
		if lineWidth := lipgloss.Width(merged); lineWidth < width {
			merged += strings.Repeat(" ", width-lineWidth)
		}
		baseLines[lineIndex] = merged
	}
	return strings.Join(baseLines, "\n")
}
