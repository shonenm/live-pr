package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/shonenm/live-pr/internal/event"
)

// GitHub Primer (dark) palette + per-kind label colors.
const (
	cFg          = "#e6edf3"
	cMuted       = "#7d8590"
	cBorder      = "#30363d"
	cCloudBorder = "#6e7681"
	cAccent      = "#2f81f7"
	cOpen        = "#238636"
	cGreenF      = "#3fb950"
	cRedF        = "#f85149"

	cDecision = "#4493f8"
	cPivot    = "#db6d28"
	cSummary  = "#a371f7"
	cCommit   = "#3fb950"
	cNote     = "#7d8590"
)

var (
	stFg     = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg))
	stMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted))
	stBold   = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg)).Bold(true)
	stGreenF = lipgloss.NewStyle().Foreground(lipgloss.Color(cGreenF))
	stRedF   = lipgloss.NewStyle().Foreground(lipgloss.Color(cRedF))
	stAccent = lipgloss.NewStyle().Foreground(lipgloss.Color(cAccent))
)

// kindLabel renders an event kind as a small colored label.
func kindLabel(k event.Kind) string {
	name, col := "note", cNote
	switch k {
	case event.Decision:
		name, col = "decision", cDecision
	case event.Pivot:
		name, col = "pivot", cPivot
	case event.Summary:
		name, col = "summary", cSummary
	case event.Commit:
		name, col = "commit", cCommit
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Bold(true).Render(name)
}
