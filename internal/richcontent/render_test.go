package richcontent

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMermaidFallbackAndReplacement(t *testing.T) {
	body := "before\n```mermaid\ngraph LR\n A-->B\n```\nafter"
	sources := MermaidSources(body)
	if len(sources) != 1 || sources[0] != "graph LR\n A-->B" {
		t.Fatalf("sources = %#v", sources)
	}
	if got := ReplaceMermaid(body, nil); got != body {
		t.Fatalf("fallback changed source: %q", got)
	}
	got := ReplaceMermaid(body, map[string]string{sources[0]: "A ──▶ B"})
	if !strings.Contains(got, "```text\nA ──▶ B\n```") || strings.Contains(got, "```mermaid") {
		t.Fatalf("replacement = %q", got)
	}
}

func TestRenderMermaidUsesOptionalCLI(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "termaid")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'A ──▶ B\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := RenderMermaid("graph LR; A-->B", 40)
	if err != nil || got != "A ──▶ B" {
		t.Fatalf("render = %q, %v", got, err)
	}
}

func TestImageColorStaysOneRepresentativeColor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{B: 255, A: 255})
	if got := imageColor(img); got != "#7f007f" {
		t.Fatalf("color = %q", got)
	}
}
