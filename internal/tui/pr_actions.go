package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/internal/github"
)

func runPRAction(action prAction, pr gh.PR) tea.Cmd {
	return func() tea.Msg {
		client := gh.New()
		var err error
		switch action {
		case mergePR:
			err = client.Merge(pr.Number, pr.HeadRefOID)
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

func (m Model) renderActionPopup() string {
	action := m.prActionRunning
	if action == noPRAction {
		action = m.pendingPRAction
	}
	label := actionLabel(action)
	message := "Continue?"
	switch action {
	case mergePR:
		message = "Merge with a merge commit?"
	case checkoutPR:
		branch := m.prActionPR.HeadRefName
		if branch == "" {
			branch = "its branch"
		}
		message = "Checkout " + branch + "?"
	case closePR:
		message = "Close without merging?"
	}
	lines := []string{
		stBold.Render(fmt.Sprintf("%s PR #%d", label, m.prActionNumber)),
		"",
		stFg.Render(message),
	}
	if m.prActionRunning != noPRAction {
		lines = append(lines, "", stAttention.Render(m.loadSpinner.View()+" "+label+" in progress…"))
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
