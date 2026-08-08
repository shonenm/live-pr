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

func TestGitHubStyleUsesPrimerForMarkdownAndSyntax(t *testing.T) {
	cfg := githubStyle()
	if cfg.Document.Color == nil || *cfg.Document.Color != "#f0f6fc" || cfg.H1.BackgroundColor != nil || cfg.Link.Color == nil || *cfg.Link.Color != "#4493f8" {
		t.Fatalf("markdown palette = %#v", cfg)
	}
	if cfg.Code.BackgroundColor == nil || *cfg.Code.BackgroundColor != "#151b23" || cfg.CodeBlock.Chroma.Keyword.Color == nil || *cfg.CodeBlock.Chroma.Keyword.Color != "#ff7b72" || cfg.CodeBlock.Chroma.LiteralString.Color == nil || *cfg.CodeBlock.Chroma.LiteralString.Color != "#a5d6ff" {
		t.Fatalf("code palette = %#v", cfg.CodeBlock)
	}
	out := Render("# Heading\n\n[link](https://example.com) `code`", 80)
	for _, old := range []string{"\x1b[38;5;39m", "\x1b[48;5;63m", "\x1b[38;5;203m"} {
		if strings.Contains(out, old) {
			t.Fatalf("render retained Glamour dark color %q: %q", old, out)
		}
	}
}

func TestRenderCachesByBodyAndWidth(t *testing.T) {
	first := Render("**cached**", 40)
	second := Render("**cached**", 40)
	if first != second {
		t.Fatal("cached render changed")
	}
}
