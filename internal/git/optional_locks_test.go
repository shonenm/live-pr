package git

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentLocalSnapshotDoesNotRewriteIndex(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile("tracked", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	// Same content with a new mtime leaves the index stat cache stale, which
	// `git status` would otherwise refresh by taking index.lock.
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes("tracked", stale, stale); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := CurrentLocalSnapshot()
	if err != nil || snapshot.Worktree.Total() != 0 {
		t.Fatalf("snapshot = %#v err=%v", snapshot, err)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("background status rewrote the index")
	}
}
