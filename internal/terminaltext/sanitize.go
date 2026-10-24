// Package terminaltext makes untrusted text safe to print in a terminal.
package terminaltext

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// SanitizeSGR preserves only numeric SGR styling, tabs, newlines, and printable
// text. Raw Git and static diff-filter output must not control the terminal.
func SanitizeSGR(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	var state byte
	for len(s) > 0 {
		seq, _, n, next := ansi.DecodeSequence(s, state, nil)
		state = next
		s = s[n:]
		if strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") &&
			strings.Trim(seq[2:len(seq)-1], "0123456789;:") == "" {
			out.WriteString(seq)
		} else {
			out.WriteString(Sanitize(seq))
		}
	}
	return out.String()
}

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
