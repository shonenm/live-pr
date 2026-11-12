package tui

import "github.com/shonenm/live-pr/v2/internal/terminaltext"

func safeText(s string) string { return terminaltext.Sanitize(s) }
