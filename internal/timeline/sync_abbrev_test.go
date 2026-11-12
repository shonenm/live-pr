package timeline

import (
	"path/filepath"
	"testing"

	"github.com/shonenm/live-pr/v2/internal/event"
	"github.com/shonenm/live-pr/v2/internal/git"
)

func TestWithCommitsMatchesAcrossAbbreviationLengths(t *testing.T) {
	events := []event.Event{
		{Kind: event.Commit, Title: "short", SHA: "abc1234"},
		{Kind: event.Commit, Title: "long", SHA: "def5678901"},
		{Kind: event.Note, Title: "note", SHA: "9999999"},
	}
	commits := []git.Commit{
		{SHA: "abc12345", Subject: "short"},
		{SHA: "def5678", Subject: "long"},
		{SHA: "abc1299", Subject: "new"},
		{SHA: "9999999", Subject: "not a commit event"},
	}
	merged := WithCommits(events, commits)
	if len(merged) != len(events)+2 {
		t.Fatalf("merged %d events, want %d: %+v", len(merged), len(events)+2, merged)
	}
	if merged[3].SHA != "abc1299" || merged[4].SHA != "9999999" {
		t.Fatalf("appended %q, %q; want abc1299, 9999999", merged[3].SHA, merged[4].SHA)
	}
}

// A longer core.abbrev is what a grown repository produces on its own.
func TestSyncCommitsIdempotentWhenAbbreviationGrows(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gitCmd(t, dir, "checkout", "-q", "-b", "feature")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	t.Chdir(dir)

	tl := filepath.Join(dir, "timeline.jsonl")
	gitCmd(t, dir, "config", "core.abbrev", "7")
	if n, err := SyncCommits(tl, "main"); err != nil || n != 2 {
		t.Fatalf("first sync = %d, %v; want 2, nil", n, err)
	}
	gitCmd(t, dir, "config", "core.abbrev", "12")
	if n, err := SyncCommits(tl, "main"); err != nil || n != 0 {
		t.Fatalf("sync after abbreviation change = %d, %v; want 0, nil", n, err)
	}
}
