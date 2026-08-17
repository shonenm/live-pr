package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	bspinner "github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/theme"
)

// GitHub Primer dark semantic colors. Like gh-dash, ordinary content stays
// primary/muted; semantic colors are reserved for GitHub-colored states.
const (
	cFg             = theme.Foreground
	cMuted          = theme.Muted
	cSelectedBg     = theme.Selected
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
	// cDescriptionBorder frames the PR description, which is the one card
	// that is neither a comment nor a verdict.
	cDescriptionBorder = theme.DoneEmphasis
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

const (
	logo = "╻  ╻╻ ╻┏━╸   ┏━┓┏━┓\n" +
		"┃  ┃┃┏┛┣╸ ╺━╸┣━┛┣┳┛\n" +
		"┗━╸╹┗┛ ┗━╸   ╹  ╹┗╸"
	logoHeight = 3
	logoWidth  = 21 // wordmark plus its right margin
)

// renderLogo draws the wordmark anchoring the left edge of the header.
func renderLogo() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(cAccent)).MarginRight(2).Render(logo)
}

// renderPane draws a rounded box with the title embedded in the top border,
// lazygit-style: focused panes get the accent border, others stay dim.
func renderPane(title, content string, width, height int, focused bool) string {
	if width < 4 || height < 2 {
		return content
	}
	borderColor, titleStyle := cBorder, stMuted.Bold(true)
	if focused {
		borderColor, titleStyle = cAccent, stAccent.Bold(true)
	}
	border := lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor))
	innerW := width - 2
	top := border.Render("╭" + strings.Repeat("─", innerW) + "╮")
	if title != "" {
		label := ansi.Truncate(" "+title+" ", max(0, width-4), "…")
		fill := width - 3 - lipgloss.Width(label)
		top = border.Render("╭─") + titleStyle.Render(label) + border.Render(strings.Repeat("─", max(0, fill))+"╮")
	}
	side := border.Render("│")
	contentW := innerW - 2
	lines := strings.Split(content, "\n")
	rows := make([]string, 0, height)
	rows = append(rows, top)
	for i := 0; i < height-2; i++ {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], contentW, "…")
		}
		if pad := contentW - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		rows = append(rows, side+" "+line+" "+side)
	}
	rows = append(rows, border.Render("╰"+strings.Repeat("─", innerW)+"╯"))
	return strings.Join(rows, "\n")
}

// footerSegment is the lualine-style mode block naming the focused pane.
func footerSegment(label string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(cAccent)).Foreground(lipgloss.Color("#0d1117")).
		Bold(true).Padding(0, 1).Render(label)
}

// stateGlyph maps a PR state to its GitHub-colored dot.
func stateGlyph(state string) (string, lipgloss.Style) {
	switch state {
	case "open":
		return "●", stGreenF
	case "draft":
		return "◌", stMuted
	case "merged":
		return "●", lipgloss.NewStyle().Foreground(lipgloss.Color(cDoneEmphasis))
	case "closed":
		return "●", stRedF
	default:
		return "●", stMuted
	}
}

// padRow truncates an already-styled row and fills the remainder with the
// row's background so full-row selection reaches the pane edge.
func padRow(row string, width int, fill lipgloss.Style) string {
	row = ansi.Truncate(row, width, "…")
	if gap := width - lipgloss.Width(row); gap > 0 {
		row += fill.Render(strings.Repeat(" ", gap))
	}
	return row
}

func newLoadSpinner() bspinner.Model {
	frames := bspinner.Spinner{Frames: []string{"●∙∙", "∙●∙", "∙∙●"}, FPS: time.Second / 6}
	return bspinner.New(bspinner.WithSpinner(frames), bspinner.WithStyle(stAttention))
}

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

// kindLabel colors each timeline event kind so the conversation feed can be
// scanned by decision flow, not just read.
func kindLabel(k event.Kind) string {
	style := stMuted
	switch k {
	case event.Decision:
		style = stAccent
	case event.Pivot:
		style = stAttention
	case event.Summary:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color(cDoneEmphasis))
	case event.Note:
		style = stGreenF
	}
	return style.Bold(true).Render(string(k))
}
