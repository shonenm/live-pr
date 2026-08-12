package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPullRequestReviewDraftUsesRepositoryState(t *testing.T) {
	root := t.TempDir()
	got := PullRequestReviewDraft(root, 42)
	if filepath.Base(got) != "42.json" || filepath.Base(filepath.Dir(got)) != "reviews" {
		t.Fatalf("PR review draft path = %q", got)
	}
}

func TestForBranchUsesUserStateOutsideRepository(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := filepath.Join(t.TempDir(), "repo")
	st := ForBranch(root, "feature/review")
	if strings.HasPrefix(st.Dir, root) || strings.Contains(st.Dir, ".live-pr") {
		t.Fatalf("store remained repository-local: %q", st.Dir)
	}
	if !strings.HasPrefix(st.Dir, filepath.Join(state, "live-pr")) {
		t.Fatalf("store escaped state root: %q", st.Dir)
	}
}

func TestMigrateLegacyState(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()
	legacyBranch := filepath.Join(root, ".live-pr", "feature-x")
	if err := os.MkdirAll(legacyBranch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyBranch, "timeline.jsonl"), []byte("event\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".live-pr", "config.toml"), []byte("[diff]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(root); err != nil {
		t.Fatal(err)
	}
	st := ForBranch(root, "feature-x")
	if _, err := os.Stat(filepath.Join(st.Dir, "timeline.jsonl")); err != nil {
		t.Fatalf("migrated timeline missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".live-pr", "feature-x")); !os.IsNotExist(err) {
		t.Fatalf("legacy branch remained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".live-pr.toml")); err != nil {
		t.Fatalf("config was not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".live-pr")); !os.IsNotExist(err) {
		t.Fatalf("legacy runtime directory remained: %v", err)
	}
}
