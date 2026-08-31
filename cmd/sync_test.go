package cmd

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/shonenm/live-pr/internal/event"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestSyncUsesCachedPRBase(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "main")
	run("switch", "-c", "release")
	run("commit", "--allow-empty", "-m", "release")
	run("switch", "-c", "feature")
	run("commit", "--allow-empty", "-m", "feature")
	t.Chdir(dir)
	st, err := store.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	cache := gh.NewCache("feature")
	cache.PR = &gh.PR{Number: 7, BaseRefName: "release"}
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSync(&out, ""); err != nil {
		t.Fatal(err)
	}
	events, err := event.Load(st.Timeline())
	if err != nil || len(events) != 1 || events[0].Title != "feature" || !strings.Contains(out.String(), "release..HEAD") {
		t.Fatalf("sync = events:%#v output:%q err=%v", events, out.String(), err)
	}
}
