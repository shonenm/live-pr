package clipboard

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestWriteOSC52EncodesClipboardEscape pins the exact OSC 52 byte sequence:
// the escape only works over tmux/SSH when the base64 payload and the
// \x1b]52;c;...\x07 framing are byte-perfect, and no local terminal exercises
// this path during development.
func TestWriteOSC52EncodesClipboardEscape(t *testing.T) {
	var buf strings.Builder
	if err := writeOSC52(&buf, "hi"); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "\x1b]52;c;aGk=\x07" {
		t.Fatalf("osc52 sequence = %q", got)
	}

	buf.Reset()
	text := "multi\nline, 非ASCII"
	if err := writeOSC52(&buf, text); err != nil {
		t.Fatal(err)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
	if buf.String() != want {
		t.Fatalf("osc52 sequence = %q, want %q", buf.String(), want)
	}
}
