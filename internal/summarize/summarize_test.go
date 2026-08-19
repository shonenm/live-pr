package summarize

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestClaudeTimesOutInsteadOfHanging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	done := make(chan error, 1)
	go func() {
		_, err := (Claude{Timeout: 100 * time.Millisecond}).Summarize("session")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("summarize error = %v, want timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Summarize did not return; the claude CLI call is not bounded by a deadline")
	}
}

func TestCommandPipesTranscriptAndParsesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	c := Command{Command: `printf '# %s\n\n- noted\n' "$(head -n 1)"`}
	got, err := c.Summarize("decided to ship\nmore transcript")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "decided to ship" || got.Body != "- noted" {
		t.Fatalf("summary = %#v", got)
	}
}

func TestCommandErrorIncludesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	c := Command{Command: "echo 'backend down' >&2; exit 1"}
	_, err := c.Summarize("session")
	if err == nil || !strings.Contains(err.Error(), "summarize command") || !strings.Contains(err.Error(), "backend down") {
		t.Fatalf("summarize error = %v", err)
	}
}

func TestCommandTimesOutInsteadOfHanging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	done := make(chan error, 1)
	go func() {
		_, err := (Command{Command: "sleep 30", Timeout: 100 * time.Millisecond}).Summarize("session")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("summarize error = %v, want timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Summarize did not return; the command is not bounded by a deadline")
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
