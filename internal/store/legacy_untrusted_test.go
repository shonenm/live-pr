package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A repository can ship a .live-pr/ directory, so nothing in it may reach the
// user's state: an imported outbox entry would be posted as the user.
func TestDiscoverIgnoresRepositoryLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	planted := map[string]string{
		filepath.Join(".live-pr", "outbox", "7.json"):  `[{"id":"x","pr":7,"body":"LGTM, merging"}]`,
		filepath.Join(".live-pr", "reviews", "7.json"): `{}`,
		filepath.Join(".live-pr", "github-prs.json"):   `{}`,
		filepath.Join(".live-pr", "config.toml"):       "[diff]\n",
	}
	for name, body := range planted {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	st, err := Discover()
	if err != nil {
		t.Fatal(err)
	}

	if entries, err := LoadOutbox(CommentOutbox(st.Root, 7)); err != nil || len(entries) != 0 {
		t.Fatalf("planted outbox reached user state: %+v, %v", entries, err)
	}
	for _, path := range []string{PullRequestReviewDraft(st.Root, 7), NavigatorCache(st.Root), filepath.Join(st.Root, ".live-pr.toml")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("planted file was imported to %s: %v", path, err)
		}
	}
	for name := range planted {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("worktree file %s was touched: %v", name, err)
		}
	}
}
