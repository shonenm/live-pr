package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func runReviewGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestReviewDraftCLIHelpersUseCurrentPRRevision(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	runReviewGit(t, repo, "init", "-b", "main")
	runReviewGit(t, repo, "config", "user.email", "test@example.com")
	runReviewGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewGit(t, repo, "add", ".")
	runReviewGit(t, repo, "commit", "-m", "init")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	st, err := store.Discover()
	if err != nil {
		t.Fatal(err)
	}
	cache := gh.NewCache("main")
	cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}

	gotStore, pr, draft, err := currentReviewDraft()
	if err != nil || store.PullRequestReviewDraft(gotStore.Root, pr.Number) != store.PullRequestReviewDraft(st.Root, pr.Number) || pr.Number != 12 || draft.Commit != "abc123" {
		t.Fatalf("current review = store:%#v pr:%#v draft:%#v err=%v", gotStore, pr, draft, err)
	}
}
