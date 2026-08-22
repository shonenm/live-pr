package markdown

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	xansi "github.com/charmbracelet/x/ansi"
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

func TestRenderReusedRendererMatchesFreshRenderer(t *testing.T) {
	const width = 60
	warm := "# Warm-up\n\n> quote\n\n1. one\n2. two\n\n```go\npackage main\n```"
	input := "# Title\n\nParagraph with **bold**, `code`, and [link](https://example.com).\n\n- item"
	// Two sequential renders at the same width exercise the reused renderer;
	// the second must match a fresh renderer, or state leaked between calls.
	_ = Render(warm, width)
	got := Render(input, width)

	fresh, err := glamour.NewTermRenderer(
		glamour.WithStyles(githubStyle()),
		glamour.WithWordWrap(glamourWrapWidth),
	)
	if err != nil {
		t.Fatalf("NewTermRenderer: %v", err)
	}
	want, err := fresh.Render(input)
	if err != nil {
		t.Fatalf("fresh Render: %v", err)
	}
	if got != wrapRendered(want, width) {
		t.Fatalf("reused renderer output diverged from fresh renderer:\ngot:  %q\nwant: %q", got, wrapRendered(want, width))
	}
}

func TestRenderCachesByBodyAndWidth(t *testing.T) {
	first := Render("**cached**", 40)
	second := Render("**cached**", 40)
	if first != second {
		t.Fatal("cached render changed")
	}
}

func TestWrapTextFillsLinesAndRespectsKinsoku(t *testing.T) {
	const width = 48
	in := "これは日本語の長い文章です。句読点や「括弧」を含んだテキストが、端末幅に対してどこで折り返されるかを確認します。英語のsome long wordsも混ぜてみます。"
	out := WrapText(in, width)
	for _, line := range strings.Split(out, "\n") {
		plain := ansi.ReplaceAllString(line, "")
		if w := xansi.StringWidth(line); w > width {
			t.Fatalf("line exceeds width %d: %q", w, plain)
		}
		if strings.ContainsRune("、。，．！？」』）〕｝〉》」】…?!%)]}.,:;", []rune(plain)[0]) {
			t.Fatalf("line starts with closing punctuation: %q", plain)
		}
		for _, word := range []string{"some", "long", "words"} {
			if !strings.Contains(out, word) {
				t.Fatalf("Latin word %q split across lines", word)
			}
		}
	}
}
