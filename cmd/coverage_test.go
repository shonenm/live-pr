package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shonenm/live-pr/internal/demo"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

// cmdRepo initializes a git repo, chdir's into it, and points XDG_STATE_HOME at
// a temp dir so store state is isolated. It returns the branch store.
func cmdRepo(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	return store.ForBranch(repo, "main")
}

// flagCmd builds a throwaway command carrying the given string flags, so a
// RunE can be exercised without mutating the package-global command state.
func flagCmd(strFlags map[string]string, intFlags map[string]int) *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	for name, val := range strFlags {
		c.Flags().String(name, val, "")
	}
	for name, val := range intFlags {
		c.Flags().Int(name, val, "")
	}
	return c
}

func TestReadTextFlagConflictAndFile(t *testing.T) {
	// Both inline and file set: conflict.
	c := flagCmd(map[string]string{"body": "", "body-file": ""}, nil)
	_ = c.Flags().Set("body", "inline")
	_ = c.Flags().Set("body-file", "some/file")
	if _, err := readTextFlag(c, "body", "body-file"); err == nil {
		t.Fatal("both --body and --body-file should conflict")
	}
	// File path: read and trim.
	dir := t.TempDir()
	file := filepath.Join(dir, "body.md")
	if err := os.WriteFile(file, []byte("  from file \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c = flagCmd(map[string]string{"body": "", "body-file": ""}, nil)
	_ = c.Flags().Set("body-file", file)
	got, err := readTextFlag(c, "body", "body-file")
	if err != nil || got != "from file" {
		t.Fatalf("file body = %q, %v", got, err)
	}
}

func TestReviewWriteCommandsPersistDraft(t *testing.T) {
	st := cmdRepo(t)
	cache := gh.NewCache("main")
	cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}
	draftPath := store.PullRequestReviewDraft(st.Root, 12)

	// body
	bodyCmd := flagCmd(map[string]string{"body": "", "body-file": ""}, nil)
	_ = bodyCmd.Flags().Set("body", "overall looks good")
	if err := reviewBodyCmd.RunE(bodyCmd, nil); err != nil {
		t.Fatal(err)
	}
	// add inline comment
	addCmd := flagCmd(map[string]string{"body": "", "body-file": "", "side": "RIGHT"}, map[string]int{"line": 3})
	_ = addCmd.Flags().Set("body", "nit")
	if err := reviewAddCmd.RunE(addCmd, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	draft, err := gh.LoadReviewDraft(draftPath, 12, "abc123")
	if err != nil || draft.Body != "overall looks good" || len(draft.Comments) != 1 || draft.Comments[0].Path != "README.md" {
		t.Fatalf("draft after writes = %#v err=%v", draft, err)
	}

	// invalid side is rejected before touching the draft
	badSide := flagCmd(map[string]string{"body": "", "body-file": "", "side": "SIDEWAYS"}, map[string]int{"line": 1})
	_ = badSide.Flags().Set("body", "x")
	if err := reviewAddCmd.RunE(badSide, []string{"README.md"}); err == nil {
		t.Fatal("invalid side should be rejected")
	}

	// delete the inline comment
	if err := reviewDeleteCmd.RunE(&cobra.Command{}, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	draft, _ = gh.LoadReviewDraft(draftPath, 12, "abc123")
	if len(draft.Comments) != 0 {
		t.Fatalf("comment not deleted: %#v", draft.Comments)
	}
	// out-of-range delete errors
	if err := reviewDeleteCmd.RunE(&cobra.Command{}, []string{"5"}); err == nil {
		t.Fatal("deleting a missing index should error")
	}
	// non-numeric index errors
	if err := reviewDeleteCmd.RunE(&cobra.Command{}, []string{"1x"}); err == nil {
		t.Fatal("non-numeric index should error")
	}
}

func TestCommentEditChangesAndRejections(t *testing.T) {
	cmdRepo(t)
	// Seed a decision comment through the add command's RunE.
	addCmd := flagCmd(map[string]string{"kind": "decision", "body": "", "body-file": "", "author": "user"}, nil)
	if err := commentAddCmd.RunE(addCmd, []string{"Chose Go"}); err != nil {
		t.Fatal(err)
	}
	_, events, err := currentTimeline()
	if err != nil || len(events) != 1 {
		t.Fatalf("seed = %#v err=%v", events, err)
	}
	id := events[0].ID

	// No field changed → error.
	if err := commentEditCmd.RunE(flagCmd(nil, nil), []string{id}); err == nil {
		t.Fatal("edit with no field should error")
	}
	// Unknown id → error.
	if err := commentEditCmd.RunE(flagCmd(map[string]string{"body": ""}, nil), []string{"nope", "title"}); err == nil {
		t.Fatal("editing an unknown id should error")
	}

	// Edit the body and title.
	editCmd := flagCmd(map[string]string{"kind": "", "body": "", "body-file": "", "author": ""}, nil)
	_ = editCmd.Flags().Set("body", "because single binary")
	if err := commentEditCmd.RunE(editCmd, []string{id, "Chose Go for real"}); err != nil {
		t.Fatal(err)
	}
	_, events, err = currentTimeline()
	if err != nil || events[0].Title != "Chose Go for real" || events[0].Body != "because single binary" {
		t.Fatalf("edit not persisted: %#v err=%v", events, err)
	}
}

func TestHookStopNeverBlocksOutsideRepo(t *testing.T) {
	// cwd points at a non-git dir: store.Discover fails and the hook returns
	// nil rather than blocking the agent.
	nonRepo := t.TempDir()
	payload := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(payload, []byte(`{"cwd":`+quote(nonRepo)+`,"transcript_path":"/nonexistent"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(payload)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old })
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if err := hookStopCmd.RunE(hookStopCmd, nil); err != nil {
		t.Fatalf("hook stop must never return an error, got %v", err)
	}
}

func quote(s string) string { return `"` + s + `"` }

func TestLoadStatusRefreshUpdatesCache(t *testing.T) {
	root := t.TempDir()
	if err := demo.CreateRepo(root, "git"); err != nil {
		t.Fatal(err)
	}
	binDir, stateDir, err := demo.SetupGitHub(root, "git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVE_PR_DEMO_STATE", stateDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldDir, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	st, err := store.Discover()
	if err != nil {
		t.Fatal(err)
	}
	// Seed a stale cache with an old timestamp; the refresh must overwrite it.
	cache := gh.NewCache(st.Branch)
	cache.FetchedAt = "2000-01-01T00:00:00Z"
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}

	status, err := loadStatus(true)
	if err != nil {
		t.Fatalf("refresh loadStatus: %v", err)
	}
	if status.CacheFetchedAt == "" || status.CacheFetchedAt == "2000-01-01T00:00:00Z" {
		t.Fatalf("refresh did not update the cache timestamp: %q", status.CacheFetchedAt)
	}
	saved, err := gh.LoadCache(st.GitHubCache(), st.Branch)
	if err != nil || saved.FetchedAt == "2000-01-01T00:00:00Z" {
		t.Fatalf("refreshed cache not persisted: %#v err=%v", saved, err)
	}
}
