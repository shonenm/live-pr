package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPullRequestReviewDraftUsesRepositoryState(t *testing.T) {
	root := t.TempDir()
	got := PullRequestReviewDraft(root, 42)
	if filepath.Base(got) != "42.json" || filepath.Base(filepath.Dir(got)) != "reviews" {
		t.Fatalf("PR review draft path = %q", got)
	}
}

func TestForBranchUsesUserStateOutsideRepository(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := filepath.Join(t.TempDir(), "repo")
	st := ForBranch(root, "feature/review")
	if strings.HasPrefix(st.Dir, root) || strings.Contains(st.Dir, ".live-pr") {
		t.Fatalf("store remained repository-local: %q", st.Dir)
	}
	if !strings.HasPrefix(st.Dir, filepath.Join(state, "live-pr")) {
		t.Fatalf("store escaped state root: %q", st.Dir)
	}
}

func TestMigrateLegacyState(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()
	legacyBranch := filepath.Join(root, ".live-pr", "feature-x")
	if err := os.MkdirAll(legacyBranch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyBranch, "timeline.jsonl"), []byte("event\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".live-pr", "config.toml"), []byte("[diff]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(root); err != nil {
		t.Fatal(err)
	}
	st := ForBranch(root, "feature-x")
	if _, err := os.Stat(filepath.Join(st.Dir, "timeline.jsonl")); err != nil {
		t.Fatalf("migrated timeline missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".live-pr", "feature-x")); !os.IsNotExist(err) {
		t.Fatalf("legacy branch remained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".live-pr.toml")); err != nil {
		t.Fatalf("config was not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".live-pr")); !os.IsNotExist(err) {
		t.Fatalf("legacy runtime directory remained: %v", err)
	}
}

func TestWriteConclusionTrimsAndReplaces(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st := ForBranch(filepath.Join(t.TempDir(), "repo"), "feature")
	if st.HasData() {
		t.Fatal("fresh store should have no data")
	}
	if err := st.WriteConclusion("  first body \n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(st.Conclusion())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first body\n" {
		t.Fatalf("conclusion = %q, want trimmed body + trailing newline", got)
	}
	if !st.HasData() {
		t.Fatal("store should report data after WriteConclusion")
	}
	info, err := os.Stat(st.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("state dir mode = %o, want 700", perm)
		}
	}
	// Overwriting replaces rather than appends.
	if err := st.WriteConclusion("second"); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(st.Conclusion())
	if err != nil || string(got) != "second\n" {
		t.Fatalf("replaced conclusion = %q, err=%v", got, err)
	}
}

func TestStateRootFallsBackByGOOSWithoutXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	root := stateRoot()
	var wantSeg string
	switch runtime.GOOS {
	case "darwin":
		wantSeg = filepath.Join("Library", "Application Support", "live-pr")
	case "windows":
		wantSeg = filepath.Join("live-pr", "state")
	default:
		wantSeg = filepath.Join(".local", "state", "live-pr")
	}
	if !strings.Contains(root, wantSeg) {
		t.Fatalf("stateRoot() = %q, want it to contain %q", root, wantSeg)
	}
}

func TestBranchSlugSeparatesSlashAndDash(t *testing.T) {
	slash := branchSlug("feat/x")
	dash := branchSlug("feat-x")
	if slash == dash {
		t.Fatalf("feat/x and feat-x share state: %q", slash)
	}
	if dash != "feat-x" {
		t.Fatalf("safe name gained a suffix: %q", dash)
	}
	if branchSlug("feat/x") == branchSlug("feat:x") {
		t.Fatal("distinct replaced names still collide")
	}
	if !strings.HasPrefix(slash, "feat-x-") {
		t.Fatalf("replaced name lost readability: %q", slash)
	}
}

func TestWorktreesShareRepositoryState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base := t.TempDir()
	main := filepath.Join(base, "repo")
	linked := filepath.Join(base, "repo-wt")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(base, "init", "-b", "main", main)
	run(main, "config", "user.email", "t@e")
	run(main, "config", "user.name", "t")
	run(main, "commit", "--allow-empty", "-m", "base")
	run(main, "worktree", "add", "-b", "feature", linked)

	fromMain := repoStateRoot(main)
	fromWorktree := repoStateRoot(linked)
	if fromMain != fromWorktree {
		t.Fatalf("worktree state split: main=%q worktree=%q", fromMain, fromWorktree)
	}
	// The main checkout keeps its historical identity: the hash of its own
	// absolute path, so existing state directories stay valid.
	clean, err := filepath.Abs(main)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(clean))
	want := filepath.Join(stateRoot(), "repos", slug(filepath.Base(clean))+"-"+hex.EncodeToString(hash[:])[:12])
	if fromMain != want {
		t.Fatalf("main checkout identity changed: got %q want %q", fromMain, want)
	}
}

func TestReviewedMarksPathScopesByPRAndBranch(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	pr := ReviewedMarksPath(root, 101, "feature/x")
	if filepath.Base(pr) != "101.json" || filepath.Base(filepath.Dir(pr)) != "reviewed" {
		t.Fatalf("PR marks path = %q", pr)
	}
	if stacked := ReviewedMarksPath(root, 102, "feature/x"); stacked == pr {
		t.Fatalf("stacked PRs on one branch share a marks file: %q", stacked)
	}
	branch := ReviewedMarksPath(root, 0, "feature/x")
	if !strings.HasPrefix(filepath.Base(branch), "branch-feature-x") || branch == pr {
		t.Fatalf("branch-scoped marks path = %q", branch)
	}
	if other := ReviewedMarksPath(root, 0, "feature/y"); other == branch {
		t.Fatalf("distinct branches share a marks file: %q", other)
	}
}

func TestReviewedMarksRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := ReviewedMarksPath(t.TempDir(), 7, "main")

	marks, err := LoadReviewedMarks(path)
	if err != nil || marks == nil || len(marks) != 0 {
		t.Fatalf("missing file marks = %#v err=%v", marks, err)
	}

	want := map[string]string{"a.go": "old:new", "dir/b.go": "x:y"}
	if err := SaveReviewedMarks(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadReviewedMarks(path)
	if err != nil || len(got) != len(want) || got["a.go"] != want["a.go"] || got["dir/b.go"] != want["dir/b.go"] {
		t.Fatalf("round-trip marks = %#v err=%v", got, err)
	}

	if err := SaveReviewedMarks(path, map[string]string{"a.go": "v2"}); err != nil {
		t.Fatal(err)
	}
	got, err = LoadReviewedMarks(path)
	if err != nil || len(got) != 1 || got["a.go"] != "v2" {
		t.Fatalf("overwritten marks = %#v err=%v", got, err)
	}

	// The atomic write must not leave its temp file behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".reviewed-") {
			t.Fatalf("temp file leaked: %s", entry.Name())
		}
	}
}

func TestLoadReviewedMarksRejectsCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marks.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReviewedMarks(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("corrupt marks error = %v", err)
	}
}
