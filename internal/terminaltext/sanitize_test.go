package terminaltext

import (
	"strings"
	"testing"
)

func TestSanitizeRemovesTerminalControls(t *testing.T) {
	input := "safe\x1b[31mred\x1b[0m\x1b]52;c;Y2xpcGJvYXJk\a\x00\x7f\u009bunsafe\nnext\tcell"
	got := Sanitize(input)
	if got != "saferedunsafe\nnext\tcell" || strings.ContainsAny(got, "\x1b\a\x00\x7f") {
		t.Fatalf("Sanitize = %q", got)
	}
}
