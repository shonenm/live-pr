package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shonenm/live-pr/internal/demo"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestLoadStatusUsesCachedPullRequest(t *testing.T) {
	root := t.TempDir()
	if err := demo.CreateRepo(root, "git"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	st, err := store.Discover()
	if err != nil {
		t.Fatal(err)
	}
	cache := gh.NewCache(st.Branch)
	cache.PR = &gh.PR{
		Number: 42, URL: "https://example.test/pull/42", Title: "Demo PR", State: "OPEN",
		BaseRefName: "main", HeadRefName: st.Branch,
	}
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}

	status, err := loadStatus(false)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.Repository != canonicalRoot || status.Branch != "demo/git" || status.Base != "main" {
		t.Fatalf("repository status = %#v", status)
	}
	if status.Target != "github" || status.PR == nil || status.PR.Number != 42 {
		t.Fatalf("pull request status = %#v", status)
	}
	if err := os.WriteFile(st.GitHubCache(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = loadStatus(false)
	if err != nil || status.Target != "local" || status.Warning == "" {
		t.Fatalf("malformed cache status = %#v err=%v", status, err)
	}
}
