package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestGitErrorIncludesOperationAndStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'fatal: deliberate failure' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	_, err := Commits("main")
	if err == nil || !strings.Contains(err.Error(), "git log") || !strings.Contains(err.Error(), "fatal: deliberate failure") {
		t.Fatalf("git error = %v", err)
	}
}

func TestFetchPullLeavesCheckoutUntouched(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone := filepath.Join(root, "clone")
	runIn := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runIn(root, "init", "--bare", origin)
	runIn(root, "init", "-b", "main", seed)
	runIn(seed, "config", "user.email", "test@example.com")
	runIn(seed, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(seed, "file"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(seed, "add", "file")
	runIn(seed, "commit", "-m", "main")
	runIn(seed, "remote", "add", "origin", origin)
	runIn(seed, "push", "origin", "main")
	runIn(seed, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(seed, "file"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(seed, "commit", "-am", "feature")
	oid := runIn(seed, "rev-parse", "HEAD")
	runIn(seed, "push", "origin", "feature")
	runIn(origin, "update-ref", "refs/pull/7/head", oid)
	runIn(root, "clone", "--branch", "main", origin, clone)
	if err := os.WriteFile(filepath.Join(clone, "dirty"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeBranch := runIn(clone, "branch", "--show-current")
	beforeStatus := runIn(clone, "status", "--porcelain")
	t.Chdir(clone)

	ref, err := FetchPull(7, "main", oid)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "refs/live-pr/pulls/7/head" || runIn(clone, "rev-parse", ref) != oid {
		t.Fatalf("fetched ref=%q", ref)
	}
	if got := runIn(clone, "branch", "--show-current"); got != beforeBranch {
		t.Fatalf("branch changed: %q", got)
	}
	if got := runIn(clone, "status", "--porcelain"); got != beforeStatus {
		t.Fatalf("worktree changed: before=%q after=%q", beforeStatus, got)
	}
}

func TestCheckMergeReadinessDoesNotTouchCheckout(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	run("switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "feature")
	run("switch", "main")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "main")
	if err := os.WriteFile(filepath.Join(dir, "dirty"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := run("status", "--porcelain")
	t.Chdir(dir)

	readiness, err := CheckMergeReadiness("main", "feature")
	if err != nil || readiness.Behind != 1 || len(readiness.ConflictFiles) != 1 || readiness.ConflictFiles[0] != "file.txt" {
		t.Fatalf("readiness = %#v err=%v", readiness, err)
	}
	if got := run("status", "--porcelain"); got != before {
		t.Fatalf("checkout changed: before=%q after=%q", before, got)
	}
}

func TestCheckMergeReadinessFindsDivergentAppendConflicts(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "plan"), []byte("shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	run("switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "plan"), []byte("shared\nours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "ours")
	run("switch", "main")
	if err := os.WriteFile(filepath.Join(dir, "plan"), []byte("shared\ntheirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "theirs")
	t.Chdir(dir)

	readiness, err := CheckMergeReadiness("main", "feature")
	if err != nil || readiness.Behind != 1 || len(readiness.ConflictFiles) != 1 || readiness.ConflictFiles[0] != "plan" {
		t.Fatalf("divergent append readiness = %#v err=%v", readiness, err)
	}
}

func TestChangedFilesAndFileDiff(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte("package old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "base")
	runGit("switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("mv", "old.go", "new.go")
	runGit("add", "file.txt")
	runGit("commit", "-m", "change")

	t.Chdir(dir)

	files, err := ChangedFilesRange("main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := HasChanges("main", "HEAD"); err != nil || !changed {
		t.Fatalf("feature changes = %v, err=%v", changed, err)
	}
	if dirty, err := HasUncommittedChanges(); err != nil || dirty {
		t.Fatalf("clean worktree = %v, err=%v", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, err := HasUncommittedChanges(); err != nil || !dirty {
		t.Fatalf("dirty worktree = %v, err=%v", dirty, err)
	}
	stats, err := DiffStats("main", "HEAD")
	if err != nil || stats.Files != 2 || stats.Additions != 1 || stats.Deletions != 1 {
		t.Fatalf("diff stats = %#v, err=%v", stats, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("after\nextra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktree, err := DiffStats("main", "")
	if err != nil || worktree.Files != 2 || worktree.Additions != 2 || worktree.Deletions != 1 {
		t.Fatalf("worktree stats = %#v, err=%v", worktree, err)
	}
	worktreeFiles, err := ChangedFilesRange("main", "")
	if err != nil || len(worktreeFiles) != 2 {
		t.Fatalf("worktree files = %#v, err=%v", worktreeFiles, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("unexpected files: %#v", files)
	}
	var modified, renamed *ChangedFile
	for i := range files {
		switch {
		case files[i].Path == "file.txt":
			modified = &files[i]
		case files[i].Path == "new.go":
			renamed = &files[i]
		}
	}
	if modified == nil || modified.Status != "M" || renamed == nil || !strings.HasPrefix(renamed.Status, "R") || renamed.OldPath != "old.go" {
		t.Fatalf("unexpected files: %#v", files)
	}
	if diff, err := FileDiffRange("main", "HEAD", "file.txt"); err != nil || !strings.Contains(diff, "-before") || !strings.Contains(diff, "after") {
		t.Fatalf("unexpected diff: %q, err=%v", diff, err)
	}
	if diff, err := FileDiffRange("main", "HEAD", renamed.OldPath, renamed.Path); err != nil || !strings.Contains(diff, "rename from old.go") || !strings.Contains(diff, "rename to new.go") {
		t.Fatalf("unexpected rename diff: %q, err=%v", diff, err)
	}
	runGit("update-ref", "refs/remotes/origin/release", "HEAD~1")
	if got := ResolveBase("release"); got != "origin/release" {
		t.Fatalf("resolved base = %q", got)
	}
	if got := ResolveBase("local-only"); got != "local-only" {
		t.Fatalf("local fallback = %q", got)
	}
	featureOID, err := run("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mainOID, err := run("rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	if mergeBase, err := MergeBase("main", "HEAD"); err != nil || mergeBase != mainOID {
		t.Fatalf("merge base = %q, want %q, err=%v", mergeBase, mainOID, err)
	}
	runGit("update-ref", "refs/live-pr/pulls/1/head", featureOID)
	runGit("switch", "main")
	if changed, err := HasChanges("main", "HEAD"); err != nil || changed {
		t.Fatalf("main changes = %v, err=%v", changed, err)
	}
	if local, err := FileDiffRange("main", "HEAD"); err != nil || local != "" {
		t.Fatalf("main...HEAD should be empty: %q, err=%v", local, err)
	}
	if remote, err := FileDiffRange("main", "refs/live-pr/pulls/1/head"); err != nil || !strings.Contains(remote, "after") {
		t.Fatalf("explicit remote diff = %q, err=%v", remote, err)
	}
	commits, err := CommitsRange("main", "refs/live-pr/pulls/1/head")
	if err != nil || len(commits) != 1 {
		t.Fatalf("explicit commits = %#v, err=%v", commits, err)
	}
}

// gitRepo creates a temp repository, chdir's into it, and returns a git runner.
func gitRepo(t *testing.T) func(args ...string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(dir)
	return run
}

func TestChangedFilesRangeFingerprints(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	for _, name := range []string{"kept.go", "edited.go", "renamed.go"} {
		if err := os.WriteFile(name, []byte("package a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "base")
	run("switch", "-c", "feature")
	if err := os.WriteFile("kept.go", []byte("package a\n// kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("edited.go", []byte("package a\n// first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("mv", "renamed.go", "moved.go")
	run("commit", "-am", "first")

	before, err := ChangedFilesRange("main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	fingerprints := map[string]string{}
	for _, f := range before {
		if f.Fingerprint == "" {
			t.Fatalf("%s has no fingerprint: %#v", f.Path, f)
		}
		fingerprints[f.Path] = f.Fingerprint
	}
	if len(fingerprints) != 3 {
		t.Fatalf("changed files = %#v", before)
	}
	for _, f := range before {
		if f.Path == "moved.go" && f.OldPath != "renamed.go" {
			t.Fatalf("rename lost its old path: %#v", f)
		}
	}

	// A second commit touches only edited.go.
	if err := os.WriteFile("edited.go", []byte("package a\n// second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "second")
	after, err := ChangedFilesRange("main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range after {
		switch f.Path {
		case "edited.go":
			if f.Fingerprint == fingerprints[f.Path] {
				t.Error("edited.go kept its fingerprint after its content changed")
			}
		default:
			if f.Fingerprint != fingerprints[f.Path] {
				t.Errorf("%s changed fingerprint despite an untouched diff", f.Path)
			}
		}
	}
}

func TestChangedFilesRangeHashesWorkingTree(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile("f.go", []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")

	// Uncommitted edits have no post-image blob, so the fingerprint must come
	// from the file's content and still track further edits.
	if err := os.WriteFile("f.go", []byte("package a\n// one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := ChangedFilesRange("HEAD", "")
	if err != nil || len(first) != 1 {
		t.Fatalf("changed files = %#v err=%v", first, err)
	}
	if err := os.WriteFile("f.go", []byte("package a\n// two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := ChangedFilesRange("HEAD", "")
	if err != nil || len(second) != 1 {
		t.Fatalf("changed files = %#v err=%v", second, err)
	}
	if first[0].Fingerprint == second[0].Fingerprint {
		t.Fatal("working-tree edits produced the same fingerprint")
	}
}

func TestChangedFilesRangeHashesFromSubdirectory(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.MkdirAll("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("f.go", []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	if err := os.WriteFile("f.go", []byte("package a\n// edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Launching from a subdirectory must still hash the uncommitted file:
	// --raw paths are repo-root relative, not cwd relative.
	t.Chdir("sub")
	files, err := ChangedFilesRange("HEAD", "")
	if err != nil || len(files) != 1 {
		t.Fatalf("changed files = %#v err=%v", files, err)
	}
	_, dst, _ := strings.Cut(files[0].Fingerprint, ":")
	if strings.Trim(dst, "0") == "" {
		t.Fatalf("uncommitted file kept the null post-image: %q", files[0].Fingerprint)
	}
}

func TestFileDiffRangeAndShowSurfaceErrors(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile("file.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")

	if out, err := Show("HEAD"); err != nil || !strings.Contains(out, "base") {
		t.Fatalf("Show(HEAD) = %q, err=%v", out, err)
	}
	if out, err := FileDiffRange("no-such-branch", "HEAD"); err == nil || !strings.Contains(err.Error(), "git diff") {
		t.Fatalf("FileDiffRange with bad base: out=%q err=%v, want git diff error", out, err)
	}
	if out, err := Show("deadbeef"); err == nil || !strings.Contains(err.Error(), "git show") {
		t.Fatalf("Show with bad sha: out=%q err=%v, want git show error", out, err)
	}
}

// TestContentConflictsMergeFileFallback drives the merge-file fallback
// directly: CheckMergeReadiness only reaches it when merge-tree exits clean,
// which the readiness tests above never do, so the branch coverage lives here.
func TestContentConflictsMergeFileFallback(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Enough untouched filler that edits to the first and last line merge
	// cleanly instead of conflicting.
	cleanBody := func(first, last string) string {
		return first + "\n" + strings.Repeat("filler\n", 8) + last + "\n"
	}
	write("conflict.txt", "shared\n")
	write("clean.txt", cleanBody("top", "bottom"))
	write("modify-delete.txt", "shared\n")
	write("delete-delete.txt", "shared\n")
	write("big.txt", "orig\n")
	run("add", ".")
	run("commit", "-m", "base")

	run("switch", "-c", "feature")
	write("conflict.txt", "ours\n")
	write("clean.txt", cleanBody("ours-top", "bottom"))
	write("modify-delete.txt", "ours\n")
	run("rm", "delete-delete.txt")
	write("big.txt", "ours\n"+strings.Repeat("x", maxContentConflictBlob+1))
	run("commit", "-am", "ours")

	run("switch", "main")
	write("conflict.txt", "theirs\n")
	write("clean.txt", cleanBody("top", "theirs-bottom"))
	run("rm", "modify-delete.txt")
	run("rm", "delete-delete.txt")
	write("big.txt", "theirs\n")
	run("commit", "-am", "theirs")

	files, err := contentConflicts("main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	// conflict.txt: merge-file reports overlapping edits. modify-delete.txt:
	// changed on one side, deleted on the other. clean.txt merges cleanly,
	// delete-delete.txt is gone on both sides, and big.txt trips the blob
	// size cap, so none of those may appear.
	want := []string{"conflict.txt", "modify-delete.txt"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("contentConflicts = %#v, want %#v", files, want)
	}
}

func TestDefaultBaseFallbackOrder(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "trunk")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile("file.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")

	if got := DefaultBase(); got != "main" {
		t.Fatalf("no origin/HEAD, no main/master = %q, want fallback main", got)
	}
	run("branch", "master")
	if got := DefaultBase(); got != "master" {
		t.Fatalf("master only = %q, want master", got)
	}
	run("branch", "main")
	if got := DefaultBase(); got != "main" {
		t.Fatalf("main and master = %q, want main preferred", got)
	}
	run("update-ref", "refs/remotes/origin/develop", "HEAD")
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	if got := DefaultBase(); got != "origin/develop" {
		t.Fatalf("origin/HEAD set = %q, want origin/develop", got)
	}
}
