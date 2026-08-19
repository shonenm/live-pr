package store

import (
	"os"
	"os/exec"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
)

// setupSessionRepo builds a real git repository on a feature branch and makes
// it the working directory so Discover resolves it.
func setupSessionRepo(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")
	runGit("commit", "--allow-empty", "-m", "base")
	runGit("switch", "-c", "feature")

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestLoadSessionWithoutCacheStartsEmpty(t *testing.T) {
	setupSessionRepo(t)
	st, cache, err := LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "feature" {
		t.Fatalf("branch = %q, want feature", st.Branch)
	}
	if cache.PR != nil || cache.Head != "feature" {
		t.Fatalf("missing cache should load empty for the branch: %#v", cache)
	}
}

func TestLoadSessionLoadsSavedBranchCache(t *testing.T) {
	setupSessionRepo(t)
	st, _, err := LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	saved := gh.NewCache("feature")
	saved.PR = &gh.PR{Number: 7, HeadRefName: "feature"}
	if err := gh.SaveCache(st.GitHubCache(), saved); err != nil {
		t.Fatal(err)
	}

	_, cache, err := LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if cache.PR == nil || cache.PR.Number != 7 {
		t.Fatalf("saved PR was not resolved: %#v", cache.PR)
	}
}

func TestLoadSessionResetsCacheFromAnotherBranch(t *testing.T) {
	setupSessionRepo(t)
	st, _, err := LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	stale := gh.NewCache("other-branch")
	stale.PR = &gh.PR{Number: 3, HeadRefName: "other-branch"}
	if err := gh.SaveCache(st.GitHubCache(), stale); err != nil {
		t.Fatal(err)
	}

	_, cache, err := LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if cache.PR != nil || cache.Head != "feature" {
		t.Fatalf("head-mismatched cache should reset: %#v", cache)
	}
}
