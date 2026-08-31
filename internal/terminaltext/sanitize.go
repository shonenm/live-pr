// Package terminaltext makes untrusted text safe to print in a terminal.
package terminaltext

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Sanitize removes terminal escape sequences and control characters while
// preserving tabs and newlines used by Markdown and multiline comments.
func Sanitize(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
