package markdown

import (
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRenderFormatsMarkdownAndKeepsMediaURLs(t *testing.T) {
	input := "**bold**\n\n- item\n\n![preview](https://example.com/a_(b).png)\n\nhttps://example.com/video.mp4\n\n```md\n![code](inside.png)\n```"
	out := ansi.ReplaceAllString(Render(input, 80), "")
	for _, want := range []string{"bold", "item", "https://example.com/a_(b).png", "https://example.com/video.mp4", "![code](inside.png)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered output missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "**bold**") {
		t.Fatalf("rendered output retained Markdown emphasis: %q", out)
	}
}

func TestRenderCachesByBodyAndWidth(t *testing.T) {
	first := Render("**cached**", 40)
	second := Render("**cached**", 40)
	if first != second {
		t.Fatal("cached render changed")
	}
}
