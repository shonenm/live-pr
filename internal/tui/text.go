package tui

import "github.com/shonenm/live-pr/internal/terminaltext"

func safeText(s string) string { return terminaltext.Sanitize(s) }
