package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestLocalReviewStateLifecycle(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "file"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	write("base\n")
	run("add", ".")
	run("commit", "-m", "base")
	run("switch", "-c", "feature")
	write("published\n")
	run("commit", "-am", "published")
	published := run("rev-parse", "HEAD")
	t.Chdir(root)
	st := store.ForBranch(root, "feature")
	cache := gh.NewCache("feature")
	cache.PR = &gh.PR{Number: 1, HeadRefName: "feature", HeadRefOID: published, BaseRefName: "main", State: "OPEN"}
	load := func() localData {
		t.Helper()
		data, err := loadLocalData(st, cache, nil)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	mode := func(data localData, remote bool) detailMode {
		m := testModel()
		m.screen, m.remote, m.cache = detailScreen, remote, data.cache
		m.localHeadOID, m.revisionRelation, m.workingTreeDirty = data.localHeadOID, data.revisionRelation, data.dirty
		return m.detailMode()
	}

	data := load()
	if data.revisionRelation != git.RevisionSynced || data.dirty || mode(data, false) != modeLive {
		t.Fatalf("published clean = relation:%v dirty:%v mode:%v", data.revisionRelation, data.dirty, mode(data, false))
	}
	if data.headRev != published || len(data.files) != 1 {
		t.Fatalf("published files = head:%q files:%#v", data.headRev, data.files)
	}
	publishedFingerprint := data.files[0].Fingerprint
	if mode(data, true) != modeRemote {
		t.Fatalf("remote target mode = %v", mode(data, true))
	}

	write("staged\n")
	run("add", "file")
	write("unstaged\n")
	data = load()
	if !data.dirty || data.worktree.Staged != 1 || data.worktree.Unstaged != 1 || mode(data, false) != modeLocal {
		t.Fatalf("dirty = %#v mode:%v", data.worktree, mode(data, false))
	}
	if len(data.files) != 1 || data.files[0].Fingerprint != publishedFingerprint {
		t.Fatalf("working tree leaked into published files: %#v", data.files)
	}

	run("add", "file")
	run("commit", "-m", "local")
	local := run("rev-parse", "HEAD")
	data = load()
	if data.revisionRelation != git.RevisionLocalAhead || data.revisionAhead != 1 || data.dirty {
		t.Fatalf("local ahead = relation:%v ahead:%d dirty:%v", data.revisionRelation, data.revisionAhead, data.dirty)
	}
	if len(data.files) != 1 || data.files[0].Fingerprint != publishedFingerprint {
		t.Fatalf("unpushed commit leaked into published files: %#v", data.files)
	}

	// Publishing moves only the PR boundary; the checkout itself is unchanged.
	cache.PR.HeadRefOID = local
	data = load()
	if data.revisionRelation != git.RevisionSynced || mode(data, false) != modeLive {
		t.Fatalf("after push = relation:%v mode:%v", data.revisionRelation, mode(data, false))
	}

	// A force-push replaces the published child of the original common commit.
	run("switch", "-c", "remote-force", published)
	run("commit", "--allow-empty", "-m", "remote replacement")
	remote := run("rev-parse", "HEAD")
	run("switch", "feature")
	cache.PR.HeadRefOID = remote
	data = load()
	if data.revisionRelation != git.RevisionDiverged || data.revisionAhead != 1 || data.revisionBehind != 1 || len(data.remoteCommits) != 1 || mode(data, false) != modeLive {
		t.Fatalf("force push = relation:%v ahead:%d behind:%d remote:%#v mode:%v", data.revisionRelation, data.revisionAhead, data.revisionBehind, data.remoteCommits, mode(data, false))
	}

	run("reset", "--hard", remote)
	data = load()
	if data.revisionRelation != git.RevisionSynced || data.dirty || mode(data, false) != modeLive {
		t.Fatalf("resynced = relation:%v dirty:%v mode:%v", data.revisionRelation, data.dirty, mode(data, false))
	}
}

func TestCheckoutRebuildsPRModelAndPersistsExplicitCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "file")
	run("commit", "-m", "base")
	run("switch", "-c", "feature")
	if err := os.WriteFile(file, []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "feature")
	head := run("rev-parse", "HEAD")
	run("switch", "main")
	t.Chdir(root)
	old, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	old.w, old.h, old.targetGeneration, old.prList.generation = 100, 30, 3, 4
	// runPRAction has completed the checkout before reconstruction begins.
	run("switch", "feature")
	pr := gh.PR{Number: 42, State: "OPEN", BaseRefName: "main", HeadRefName: "feature", HeadRefOID: head}
	msg := rebuildAfterCheckout("test", old.targetGeneration, prActionDone{number: 42, pr: pr})().(checkoutReloaded)
	if msg.err != nil || msg.next == nil {
		t.Fatalf("checkout rebuild = next:%v err:%v", msg.next, msg.err)
	}
	next, cmd := old.handleCheckoutReloaded(msg)
	defer next.close()
	cached, cacheErr := gh.LoadCache(store.ForBranch(root, "feature").GitHubCache(), "feature")
	if cmd == nil || cacheErr != nil || cached.PR == nil || cached.PR.Number != 42 || !cached.ExplicitCheckout || next.currentBranch != "feature" || next.screen != detailScreen || next.cache.PR == nil || next.cache.PR.Number != 42 || !next.cache.ExplicitCheckout || next.w != 100 || next.h != 30 || next.targetGeneration != 4 || next.prList.generation != 5 || next.notice != "PR #42 loaded" {
		t.Fatalf("checkout model = branch:%q screen:%v cache:%#v disk:%#v cacheErr:%v size:%dx%d generations:%d/%d notice:%q cmd:%v", next.currentBranch, next.screen, next.cache, cached, cacheErr, next.w, next.h, next.targetGeneration, next.prList.generation, next.notice, cmd)
	}
}

func TestExternalBranchSwitchRebuildsLocalModel(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "file")
	run("commit", "-m", "base")
	run("switch", "-c", "feature-a")
	if err := os.WriteFile(file, []byte("feature a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "feature a")
	t.Chdir(root)
	old, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	old.w, old.h, old.targetGeneration, old.prList.generation = 120, 40, 7, 5
	run("switch", "main")
	run("switch", "-c", "feature-b")
	if err := os.WriteFile(file, []byte("feature b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "feature b")
	msg := rebuildForLocalBranchChange("test", old.targetGeneration)().(localBranchReloaded)
	if msg.err != nil || msg.next == nil {
		t.Fatalf("branch rebuild = next:%v err:%v", msg.next, msg.err)
	}
	next, cmd := old.handleLocalBranchReloaded(msg)
	defer next.close()
	if cmd == nil || next.currentBranch != "feature-b" || next.screen != detailScreen || next.w != 120 || next.h != 40 || next.targetGeneration != 8 || next.prList.generation != 6 || next.notice != "Checked-out branch changed" {
		t.Fatalf("reloaded model = branch:%q screen:%v size:%dx%d generations:%d/%d notice:%q cmd:%v", next.currentBranch, next.screen, next.w, next.h, next.targetGeneration, next.prList.generation, next.notice, cmd)
	}
}
