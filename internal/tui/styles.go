package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	bspinner "charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/theme"
)

// Semantic colors, chosen once at startup from the configured theme preset
// (primer-dark by default). Like gh-dash, ordinary content stays
// primary/muted; semantic colors are reserved for GitHub-colored states.
var (
	cFg             string
	cMuted          string
	cSelectedBg     string
	cBorder         string
	cCloudBorder    string
	cAccent         string
	cOpen           string
	cGreenF         string
	cAttention      string
	cRedF           string
	cClosed         string
	cDangerEmphasis string
	cDoneEmphasis   string
	// cDescriptionBorder frames the PR description. It is the one card that
	// carries no status, so it takes the bright neutral rather than a
	// semantic color that would read as a verdict.
	cDescriptionBorder string
	// cPageBg is the canvas the palette assumes. The TUI never paints it,
	// but emphasisInk uses it as the candidate dark ink on filled blocks.
	cPageBg string
)

var (
	stFg        lipgloss.Style
	stMuted     lipgloss.Style
	stBold      lipgloss.Style
	stGreenF    lipgloss.Style
	stAttention lipgloss.Style
	stRedF      lipgloss.Style
	stAccent    lipgloss.Style
)

func init() { applyTheme(theme.PrimerDark()) }

// applyTheme rebuilds the semantic colors and shared styles from a palette.
// New calls it once, after the config names a theme and before any style is
// captured into a model; there is no dynamic switching.
func applyTheme(p theme.Palette) {
	cFg = p.Foreground
	cMuted = p.Muted
	cSelectedBg = p.Selected
	cBorder = p.Border
	cCloudBorder = p.BorderEmphasis
	cAccent = p.Accent
	cOpen = p.OpenEmphasis
	cGreenF = p.Success
	cAttention = p.Attention
	cRedF = p.Danger
	cClosed = p.BorderEmphasis
	cDangerEmphasis = p.DangerEmphasis
	cDoneEmphasis = p.DoneEmphasis
	cDescriptionBorder = p.Foreground
	cPageBg = p.Background

	stFg = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg))
	stMuted = lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted))
	stBold = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg)).Bold(true)
	stGreenF = lipgloss.NewStyle().Foreground(lipgloss.Color(cGreenF))
	stAttention = lipgloss.NewStyle().Foreground(lipgloss.Color(cAttention))
	stRedF = lipgloss.NewStyle().Foreground(lipgloss.Color(cRedF))
	stAccent = lipgloss.NewStyle().Foreground(lipgloss.Color(cAccent))
}

// emphasisInk picks the text color for a filled block: the palette's page
// background when it reads better on the fill, white otherwise.
func emphasisInk(background string) string {
	if theme.ContrastRatio(background, cPageBg) > theme.ContrastRatio(background, "#ffffff") {
		return cPageBg
	}
	return "#ffffff"
}

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

// footerSegment is the lualine-style mode block naming the current data source.
func footerSegment(label, color string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(color)).Foreground(lipgloss.Color(emphasisInk(color))).
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
