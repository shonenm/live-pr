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

func TestSplitMarkdownPartsIsolatesTablesAndSkipsFences(t *testing.T) {
	src := "# Heading\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n```\n| x | y |\n| --- | --- |\n```\n\nend"
	parts := splitMarkdownParts(src)
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3: %#v", len(parts), parts)
	}
	if parts[0].table || !strings.Contains(parts[0].text, "# Heading") {
		t.Fatalf("first part should be prose heading: %#v", parts[0])
	}
	if !parts[1].table || !strings.Contains(parts[1].text, "| 1 | 2 |") {
		t.Fatalf("second part should be the GFM table: %#v", parts[1])
	}
	if parts[2].table || !strings.Contains(parts[2].text, "```") || !strings.Contains(parts[2].text, "| x | y |") {
		t.Fatalf("fenced table should stay prose: %#v", parts[2])
	}
}

func TestRenderWrapsTableCellsToPaneWidth(t *testing.T) {
	const width = 72
	input := `# 観測境界

| 境界 | レベル | stage / 目印 | 分かること |
| --- | --- | --- | --- |
| 準備・compaction・token mint | warn | activity + 初回の logSessionTurnActivityFailure | この試行の失敗。リトライし得る。 failureType と errorType |
| ベンダー SSE | warn | vendor_stream | logLlmVendorFailure 。 silentMs  /  lastEventType 。HTTP 失敗も 200 後の途絶も warn |
| Valkey へ frame 書き | warn | valkey_publish | frame_conflict  または  publish_failed 。再 throw する |
| 画面 SSE 読み（遅れ） | warn | bff_sse | overflow / stalled。reconnect 前提 |
| 画面 SSE 読み（flush / setup） | error | bff_sse | flush 失敗または購読 setup 失敗。この読者を落とす |

本文の続き。`

	out := ansi.ReplaceAllString(Render(input, width), "")
	if strings.Contains(out, "**") {
		t.Fatalf("table render retained markdown: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if w := xansi.StringWidth(line); w > width {
			t.Fatalf("line exceeds width %d (%d): %q", width, w, line)
		}
	}
	for _, want := range []string{"境界", "レベル", "vendor_stream", "bff_sse", "frame_conflict", "本文の続き"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered output missing %q: %q", want, out)
		}
	}
	dashOnly := 0
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed, "┼│") {
			dashOnly = 0
			continue
		}
		if strings.IndexFunc(trimmed, func(r rune) bool { return r != '─' && r != '-' && r != ' ' }) >= 0 {
			dashOnly = 0
			continue
		}
		dashOnly++
		if dashOnly >= 2 {
			t.Fatalf("separator row was sliced across lines: %q", out)
		}
	}
}

func TestRenderStillKinsokuWrapsProseBesideTable(t *testing.T) {
	const width = 48
	input := "これは日本語の長い文章です。句読点や「括弧」を含んだテキストが、端末幅に対してどこで折り返されるかを確認します。\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n"
	out := ansi.ReplaceAllString(Render(input, width), "")
	if !strings.Contains(out, "1") || !strings.Contains(out, "これは日本語") {
		t.Fatalf("missing prose or table: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		plain := strings.TrimSpace(ansi.ReplaceAllString(line, ""))
		if plain == "" {
			continue
		}
		if w := xansi.StringWidth(line); w > width {
			t.Fatalf("line exceeds width %d: %q", w, plain)
		}
		if strings.ContainsRune("、。，．！？」』）〕｝〉》」】…?!%)]}.,:;", []rune(plain)[0]) {
			t.Fatalf("line starts with closing punctuation: %q", plain)
		}
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
