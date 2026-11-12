package github

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shonenm/live-pr/v2/internal/atomicfile"
)

func TestLoadReviewDraftMovesUnreadableFileAside(t *testing.T) {
	for name, content := range map[string]string{
		"truncated":       "",
		"malformed":       "not json",
		"unknown version": `{"version":999,"pr":12,"commit_id":"abc123","body":"keep me"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "12.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			draft, err := LoadReviewDraft(path, 12, "abc123")
			var quarantined *atomicfile.QuarantinedError
			if !errors.As(err, &quarantined) {
				t.Fatalf("err = %v; want a QuarantinedError", err)
			}
			if draft.PR != 12 || draft.Commit != "abc123" || draft.Body != "" || len(draft.Comments) != 0 {
				t.Fatalf("draft = %#v; want a fresh draft", draft)
			}
			if got, err := os.ReadFile(quarantined.Path); err != nil || string(got) != content {
				t.Fatalf("moved file = %q, %v; want the original content", got, err)
			}
			// The bad file no longer blocks drafting.
			draft.Body = "second attempt"
			if err := SaveReviewDraft(path, draft); err != nil {
				t.Fatal(err)
			}
			if got, err := LoadReviewDraft(path, 12, "abc123"); err != nil || got.Body != "second attempt" {
				t.Fatalf("reload = %#v, %v", got, err)
			}
		})
	}
}
