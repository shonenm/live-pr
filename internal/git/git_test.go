package git

import (
	"os"
	"os/exec"
	"path/filepath"
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
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(clone); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

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
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

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
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

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

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	files, err := ChangedFiles("main")
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
	if diff := FileDiff("main", "file.txt"); !strings.Contains(diff, "-before") || !strings.Contains(diff, "after") {
		t.Fatalf("unexpected diff: %q", diff)
	}
	if diff := FileDiff("main", renamed.OldPath, renamed.Path); !strings.Contains(diff, "rename from old.go") || !strings.Contains(diff, "rename to new.go") {
		t.Fatalf("unexpected rename diff: %q", diff)
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
	if local := FileDiff("main"); local != "" {
		t.Fatalf("main...HEAD should be empty: %q", local)
	}
	if remote := FileDiffRange("main", "refs/live-pr/pulls/1/head"); !strings.Contains(remote, "after") {
		t.Fatalf("explicit remote diff = %q", remote)
	}
	commits, err := CommitsRange("main", "refs/live-pr/pulls/1/head")
	if err != nil || len(commits) != 1 {
		t.Fatalf("explicit commits = %#v, err=%v", commits, err)
	}
}
