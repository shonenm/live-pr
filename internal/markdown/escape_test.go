package markdown

import (
	"strings"
	"testing"
)

// Goldmark decodes numeric character references after the input has been
// sanitized, so the rendered output must not carry the decoded controls.
func TestRenderStripsEntityEncodedControlSequences(t *testing.T) {
	for name, body := range map[string]string{
		"DCS":    "hello &#27;P@kitty-cmd&#27;&#92; world",
		"APC":    "hello &#27;_Ga=T;AAAA&#27;&#92; world",
		"OSC 8":  "hello &#27;]8;;https://evil.example/&#27;&#92;https://github.com/safe&#27;]8;;&#27;&#92; world",
		"OSC 52": "hello &#27;]52;c;ZWNobyBwd25lZA==&#7; world",
		"hex":    "hello &#x1b;P@kitty-cmd&#x1b;&#x5c; world",
		"C1":     "hello &#144;@kitty-cmd&#156; world",
		"CSI":    "hello &#27;[2J world",
	} {
		t.Run(name, func(t *testing.T) {
			out := Render(body, 80)
			if strings.Contains(out, "\x1b[2J") {
				t.Fatalf("Render(%q) leaked a non-SGR CSI: %q", body, out)
			}
			rest := csiSeq.ReplaceAllString(out, "")
			if strings.ContainsAny(rest, "\x1b\a\u0090\u009c") {
				t.Fatalf("Render(%q) leaked a control sequence: %q", body, out)
			}
			if !strings.Contains(rest, "hello") || !strings.Contains(rest, "world") {
				t.Fatalf("Render(%q) lost the surrounding text: %q", body, out)
			}
		})
	}
}

func TestRenderKeepsStyling(t *testing.T) {
	out := Render("**bold** and [link](https://example.com)", 80)
	if !csiSeq.MatchString(out) {
		t.Fatalf("Render dropped SGR styling: %q", out)
	}
	if !strings.Contains(csiSeq.ReplaceAllString(out, ""), "https://example.com") {
		t.Fatalf("Render dropped the link target: %q", out)
	}
}
