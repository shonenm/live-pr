package timeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shonenm/live-pr/internal/event"
)

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSyncCommitsIdempotent(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gitCmd(t, dir, "checkout", "-q", "-b", "feat")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "first change")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "second change")

	// git helpers use the process cwd
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	tl := filepath.Join(dir, "timeline.jsonl")

	n, err := SyncCommits(tl, "main")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("first sync: want 2 new commits, got %d", n)
	}

	// Running again must add nothing (idempotent by sha).
	if n, err = SyncCommits(tl, "main"); err != nil || n != 0 {
		t.Fatalf("second sync: want 0/nil, got %d/%v", n, err)
	}

	evs, err := event.Load(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Title != "first change" || evs[1].Title != "second change" {
		t.Fatalf("expected oldest-first commit events, got %+v", evs)
	}
	for _, e := range evs {
		if e.Kind != event.Commit || e.SHA == "" {
			t.Errorf("event should be a commit with a sha: %+v", e)
		}
	}
}
