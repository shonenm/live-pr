package git

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestLinkedWorktreeCommonDirIsAbsoluteFromRelativePath(t *testing.T) {
	root := t.TempDir()
	repo, linked := filepath.Join(root, "repo"), filepath.Join(root, "linked")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, repo, "init", "-b", "main")
	runGitAt(t, repo, "config", "user.email", "test@example.com")
	runGitAt(t, repo, "config", "user.name", "Test")
	runGitAt(t, repo, "commit", "--allow-empty", "-m", "base")
	runGitAt(t, repo, "worktree", "add", "-b", "feature", linked)
	t.Chdir(root)
	mainDir, err := CommonDir("repo")
	if err != nil {
		t.Fatal(err)
	}
	linkedDir, err := CommonDir("linked")
	if err != nil || !filepath.IsAbs(mainDir) || mainDir != linkedDir {
		t.Fatalf("common dirs = %q %q err=%v", mainDir, linkedDir, err)
	}
}

func TestDetachedSnapshotAndUnicodePath(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.Mkdir("dir with space", 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("dir with space", "日本語.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	run("checkout", "--detach")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CurrentLocalSnapshot()
	if err != nil || snapshot.State.Branch != "HEAD" || snapshot.Worktree.Unstaged != 1 {
		t.Fatalf("detached snapshot = %#v err=%v", snapshot, err)
	}
	files, err := ChangedFilesRange("HEAD", "")
	if err != nil || len(files) != 1 || files[0].Path != filepath.ToSlash(path) {
		t.Fatalf("unicode files = %#v err=%v", files, err)
	}
}

func TestShallowPullRefSupportsLocalReview(t *testing.T) {
	root := t.TempDir()
	origin, seed, clone := filepath.Join(root, "origin.git"), filepath.Join(root, "seed"), filepath.Join(root, "clone")
	if err := os.Mkdir(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, root, "init", "--bare", origin)
	runGitAt(t, seed, "init", "-b", "main")
	runGitAt(t, seed, "config", "user.email", "test@example.com")
	runGitAt(t, seed, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(seed, "file"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, seed, "add", ".")
	runGitAt(t, seed, "commit", "-m", "base")
	runGitAt(t, seed, "remote", "add", "origin", origin)
	runGitAt(t, seed, "push", "origin", "main")
	runGitAt(t, seed, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(seed, "file"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, seed, "commit", "-am", "feature")
	head := runGitAt(t, seed, "rev-parse", "HEAD")
	runGitAt(t, seed, "push", "origin", "feature")
	runGitAt(t, root, "--git-dir", origin, "update-ref", "refs/pull/7/head", head)
	originPath := filepath.ToSlash(origin)
	if filepath.VolumeName(origin) != "" {
		originPath = "/" + originPath
	}
	originURL := (&url.URL{Scheme: "file", Path: originPath}).String()
	runGitAt(t, root, "clone", "--depth=1", "--branch", "feature", originURL, clone)
	t.Chdir(clone)
	ref, oid, err := FetchPull(7, "main")
	if err != nil || oid != head {
		t.Fatalf("FetchPull = %q %q err=%v", ref, oid, err)
	}
	files, err := ChangedFilesRange("origin/main", "")
	if err != nil || len(files) != 1 || files[0].Path != "file" {
		t.Fatalf("shallow local files = %#v err=%v", files, err)
	}
}
