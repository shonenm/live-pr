package summarize

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClaudeErrorIncludesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'model unavailable' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	_, err := (Claude{}).Summarize("session")
	if err == nil || !strings.Contains(err.Error(), "claude summarize") || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("summarize error = %v", err)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantBody  string
	}{
		{name: "summary", input: "\n# Keep cache-first startup\n\n- Added lazy previews\n- Preserved stale-result guards\n", wantTitle: "Keep cache-first startup", wantBody: "- Added lazy previews\n- Preserved stale-result guards"},
		{name: "plain title", input: "Ship the fix\n\n- Kept the body", wantTitle: "Ship the fix", wantBody: "- Kept the body"},
		{name: "multibyte", input: "## 日本語タイトル\n\n- 本文", wantTitle: "日本語タイトル", wantBody: "- 本文"},
		{name: "empty", input: " \n", wantTitle: "", wantBody: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.input)
			if got.Title != tt.wantTitle || got.Body != tt.wantBody {
				t.Fatalf("Parse() = %#v, want title=%q body=%q", got, tt.wantTitle, tt.wantBody)
			}
		})
	}
}
