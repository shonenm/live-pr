package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/shonenm/live-pr/internal/event"
)

// GitHub Primer (dark) palette — matches the validated fzf mock.
const (
	cFg     = "#e6edf3"
	cMuted  = "#8b949e"
	cSubtle = "#21262d"
	cBorder = "#30363d"
	cGreen  = "#238636"
	cGreenF = "#3fb950"
	cRedF   = "#f85149"
	cBlue   = "#1f6feb"
	cYellow = "#9e7110"
	cMag    = "#8957e5"
	cGrey   = "#484f58"
	cInk    = "#0d1117"
)

var (
	stFg     = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg))
	stMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted))
	stBold   = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg)).Bold(true)
	stGreenF = lipgloss.NewStyle().Foreground(lipgloss.Color(cGreenF))

	// comment-card header bar in the preview
	stCardBar = lipgloss.NewStyle().Background(lipgloss.Color(cSubtle)).Foreground(lipgloss.Color(cFg))
)

// pill renders a kind label as a GitHub-style badge.
func pill(label, bg string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color(cInk)).
		Padding(0, 1).
		Render(label)
}

// kindMeta returns the display label and pill background for an event kind.
func kindMeta(k event.Kind) (label, bg string) {
	switch k {
	case event.Decision:
		return "DECISION", cYellow
	case event.Pivot:
		return "PIVOT", cMag
	case event.Commit:
		return "COMMIT", cGreen
	case event.Summary:
		return "SUMMARY", cBlue
	default:
		return "NOTE", cGrey
	}
}
