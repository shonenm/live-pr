package terminaltext

import (
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeSGR(t *testing.T) {
	for _, control := range []string{
		"\x1b]52;c;cGF5bG9hZA==\a", "\x1b]0;title\x1b\\",
		"\x1b]8;;https://example.com\x1b\\", "\x1bPpayload\x1b\\",
		"\x1b[2J", "\x1b[?25l", "\x1b[1$m", "\x00\r\b\x7f",
		"\x9d52;c;cGF5bG9hZA==\a", "\x1b]unterminated",
	} {
		t.Run(fmt.Sprintf("%q", control), func(t *testing.T) {
			prefix := "\x1b[31m日本語\x1b[0m\n\t"
			if got := SanitizeSGR(prefix + control); got != prefix {
				t.Fatalf("SanitizeSGR = %q, want %q", got, prefix)
			}
		})
	}
	for _, sgr := range []string{"\x1b[m", "\x1b[38;2;10;20;30m", "\x1b[38:2::10:20:30m"} {
		if got := SanitizeSGR(sgr + "text"); got != sgr+"text" {
			t.Fatalf("lost SGR: %q", got)
		}
	}
}

func TestSanitizeRemovesTerminalControls(t *testing.T) {
	input := "safe\x1b[31mred\x1b[0m\x1b]52;c;Y2xpcGJvYXJk\a\x00\x7f\u009bunsafe\nnext\tcell"
	got := Sanitize(input)
	if got != "saferedunsafe\nnext\tcell" || strings.ContainsAny(got, "\x1b\a\x00\x7f") {
		t.Fatalf("Sanitize = %q", got)
	}
}
