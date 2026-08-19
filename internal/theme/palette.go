package theme

import "strings"

// Palette assigns the semantic colors the TUI is drawn with. Every preset
// keeps the same meaning per field — open stays green, merged purple, closed
// red — mapped onto that palette's official hex values.
type Palette struct {
	// Foreground is the primary text color.
	Foreground string
	// Muted is secondary text: help lines, metadata, draft states.
	Muted string
	// Background is the terminal canvas the palette is designed against.
	// The TUI never paints it, but filled blocks pick their ink from it.
	Background string
	// Selected is the full-row selection background.
	Selected string
	// Border frames unfocused panes and ordinary cards.
	Border string
	// BorderEmphasis is the brighter neutral: review-comment card borders
	// and stateless badges.
	BorderEmphasis string
	// Accent marks focus, links, and decisions.
	Accent string
	// Success is the open/positive foreground.
	Success string
	// Attention is the pending/warning color.
	Attention string
	// Danger is the closed/negative foreground.
	Danger string
	// OpenEmphasis fills the open-state badge.
	OpenEmphasis string
	// DangerEmphasis fills the closed-state badge and destructive prompts.
	DangerEmphasis string
	// DoneEmphasis is the merged/done purple.
	DoneEmphasis string
}

// ByName returns the preset for a config theme name: primer-dark,
// primer-light, nord, or catppuccin-mocha. Empty and unknown names fall back
// to primer-dark so an old or mistyped config still starts, looking as before.
func ByName(name string) Palette {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "primer-light":
		return PrimerLight()
	case "nord":
		return Nord()
	case "catppuccin-mocha":
		return CatppuccinMocha()
	default:
		return PrimerDark()
	}
}

// PrimerDark is the default: GitHub Primer dark, identical to the colors
// live-pr shipped with.
func PrimerDark() Palette {
	return Palette{
		Foreground:     Foreground,
		Muted:          Muted,
		Background:     "#0d1117", // bgColor-default
		Selected:       Selected,
		Border:         Border,
		BorderEmphasis: BorderEmphasis,
		Accent:         Accent,
		Success:        Success,
		Attention:      Attention,
		Danger:         Danger,
		OpenEmphasis:   OpenEmphasis,
		DangerEmphasis: DangerEmphasis,
		DoneEmphasis:   DoneEmphasis,
	}
}

// PrimerLight mirrors PrimerDark with GitHub Primer light functional tokens.
func PrimerLight() Palette {
	return Palette{
		Foreground:     "#1f2328", // fgColor-default
		Muted:          "#59636e", // fgColor-muted
		Background:     "#ffffff", // bgColor-default
		Selected:       "#e6eaef", // control-bgColor-active
		Border:         "#d1d9e0", // borderColor-default
		BorderEmphasis: "#818b98", // borderColor-emphasis
		Accent:         "#0969da", // fgColor-accent
		Success:        "#1a7f37", // fgColor-success
		Attention:      "#9a6700", // fgColor-attention
		Danger:         "#d1242f", // fgColor-danger
		OpenEmphasis:   "#1f883d", // bgColor-open-emphasis
		DangerEmphasis: "#cf222e", // bgColor-danger-emphasis
		DoneEmphasis:   "#8250df", // fgColor-done
	}
}

// Nord maps the official Nord palette (nordtheme.com): Polar Night for
// surfaces, Snow Storm for text, Frost for UI accents, Aurora for states.
func Nord() Palette {
	return Palette{
		Foreground:     "#d8dee9", // nord4
		Muted:          "#81a1c1", // nord9
		Background:     "#2e3440", // nord0
		Selected:       "#3b4252", // nord1
		Border:         "#4c566a", // nord3
		BorderEmphasis: "#5e81ac", // nord10
		Accent:         "#88c0d0", // nord8
		Success:        "#a3be8c", // nord14
		Attention:      "#ebcb8b", // nord13
		Danger:         "#bf616a", // nord11
		OpenEmphasis:   "#a3be8c", // nord14
		DangerEmphasis: "#bf616a", // nord11
		DoneEmphasis:   "#b48ead", // nord15
	}
}

// CatppuccinMocha maps the official Catppuccin Mocha palette
// (catppuccin.com): Base/Surface for surfaces, Text/Subtext for content,
// and the accent colors for states.
func CatppuccinMocha() Palette {
	return Palette{
		Foreground:     "#cdd6f4", // Text
		Muted:          "#a6adc8", // Subtext0
		Background:     "#1e1e2e", // Base
		Selected:       "#313244", // Surface0
		Border:         "#45475a", // Surface1
		BorderEmphasis: "#6c7086", // Overlay0
		Accent:         "#89b4fa", // Blue
		Success:        "#a6e3a1", // Green
		Attention:      "#f9e2af", // Yellow
		Danger:         "#f38ba8", // Red
		OpenEmphasis:   "#a6e3a1", // Green
		DangerEmphasis: "#f38ba8", // Red
		DoneEmphasis:   "#cba6f7", // Mauve
	}
}
