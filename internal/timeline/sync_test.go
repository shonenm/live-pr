package timeline

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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

func TestSyncCommitsConcurrentProcesses(t *testing.T) {
	if path := os.Getenv("LIVE_PR_TEST_SYNC_PATH"); path != "" {
		if _, err := SyncCommits(path, "main"); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gitCmd(t, dir, "checkout", "-q", "-b", "feature")
	for i := 0; i < 8; i++ {
		gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprint(i))
	}
	path := filepath.Join(dir, "timeline.jsonl")
	if err := event.Append(path, event.New(event.Note, "keep note", "")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSyncCommitsConcurrentProcesses$")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "LIVE_PR_TEST_SYNC_PATH="+path)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Run(); err != nil {
				t.Errorf("child: %v %s", err, output.String())
			}
		})
	}
	wg.Wait()
	events, err := event.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 || events[0].Title != "keep note" {
		t.Fatalf("events = %+v", events)
	}
	seen := map[string]bool{}
	for _, ev := range events[1:] {
		if ev.SHA == "" || seen[ev.SHA] {
			t.Fatalf("duplicate or empty SHA: %+v", ev)
		}
		seen[ev.SHA] = true
	}
}

func TestSyncCommitsLockFailure(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	t.Chdir(dir)
	path := filepath.Join(dir, "timeline.jsonl")
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncCommits(path, "main"); err == nil {
		t.Fatal("lock error lost")
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
