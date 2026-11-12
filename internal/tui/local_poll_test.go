package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPollObservesBranchWithoutScanningWorktree(t *testing.T) {
	t.Chdir(t.TempDir())
	modeGit(t, "init", "-q", "-b", "main")
	modeGit(t, "config", "user.email", "test@example.com")
	modeGit(t, "config", "user.name", "Test")
	modeWrite(t, "base\n")
	modeGit(t, "add", ".")
	modeGit(t, "commit", "-q", "-m", "base")
	modeGit(t, "checkout", "-q", "-b", "feature")
	// A worktree scan has to read the index; observing the checkout does not.
	if err := os.WriteFile(filepath.Join(".git", "index"), []byte("not an index"), 0o600); err != nil {
		t.Fatal(err)
	}

	msg, ok := pollLocalState(7)().(localStatePolled)
	if !ok || msg.err != nil || msg.generation != 7 || msg.state.Branch != "feature" {
		t.Fatalf("poll = %#v", msg)
	}

	if err := os.Remove(filepath.Join(".git", "index")); err != nil {
		t.Fatal(err)
	}
	modeGit(t, "reset", "-q")
	modeGit(t, "checkout", "-q", "--detach", "HEAD")
	msg, _ = pollLocalState(7)().(localStatePolled)
	if msg.err != nil || msg.state.Branch != "HEAD" {
		t.Fatalf("detached poll = %#v", msg)
	}
}
