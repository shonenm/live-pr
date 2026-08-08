package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/lipgloss"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/theme"
)

// GitHub Primer dark semantic colors. Like gh-dash, ordinary content stays
// primary/muted; semantic colors are reserved for GitHub-colored states.
const (
	cFg             = theme.Foreground
	cMuted          = theme.Muted
	cBorder         = theme.Border
	cCloudBorder    = theme.BorderEmphasis
	cAccent         = theme.Accent
	cOpen           = theme.OpenEmphasis
	cGreenF         = theme.Success
	cAttention      = theme.Attention
	cRedF           = theme.Danger
	cClosed         = theme.BorderEmphasis
	cDangerEmphasis = theme.DangerEmphasis
	cDoneEmphasis   = theme.DoneEmphasis
)

var (
	stFg        = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg))
	stMuted     = lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted))
	stBold      = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg)).Bold(true)
	stGreenF    = lipgloss.NewStyle().Foreground(lipgloss.Color(cGreenF))
	stAttention = lipgloss.NewStyle().Foreground(lipgloss.Color(cAttention))
	stRedF      = lipgloss.NewStyle().Foreground(lipgloss.Color(cRedF))
	stAccent    = lipgloss.NewStyle().Foreground(lipgloss.Color(cAccent))
)

func newHelp() help.Model {
	h := help.New()
	h.Styles.ShortKey = stFg
	h.Styles.FullKey = stFg
	h.Styles.ShortDesc = stMuted
	h.Styles.FullDesc = stMuted
	h.Styles.ShortSeparator = stMuted
	h.Styles.FullSeparator = stMuted
	h.Styles.Ellipsis = stMuted
	return h
}

func kindLabel(k event.Kind) string {
	return stMuted.Bold(true).Render(string(k))
}
