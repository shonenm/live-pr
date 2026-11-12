//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/store"
)

// Windows has no embedded reviewer to keep, so this runs elsewhere only.
//
// The metadata refresh right after startup rescans the same review; it must
// not throw away the running reviewer and the diff caches.
func TestLocalReloadKeepsReviewerUntilTheReviewChanges(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
	run("switch", "-c", "feature")
	if err := os.WriteFile(file, []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "feature")
	t.Chdir(root)

	// The reviewer is never started here, so no process is spawned.
	m, err := New("test", WithDiff("cat"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.close() }()
	st := store.ForBranch(m.root, m.currentBranch)
	reload := func() {
		t.Helper()
		data, err := loadLocalData(st, m.cache, m.cache.PR, m.detailView.base)
		if err != nil {
			t.Fatal(err)
		}
		m.applyLocal(st, data)
	}
	started := m.diffTerminal
	if started == nil {
		t.Fatal("startup built no reviewer")
	}
	m.detailView.rawCache["file"] = "cached diff"

	reload()
	if m.diffTerminal != started || m.detailView.rawCache["file"] != "cached diff" {
		t.Fatalf("unchanged reload = same reviewer:%v cache:%q", m.diffTerminal == started, m.detailView.rawCache["file"])
	}

	// `r` asks for a restart even though nothing changed.
	m.refreshing = false
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = updated.(Model)
	reload()
	restarted := m.diffTerminal
	if restarted == started || len(m.detailView.rawCache) != 0 || m.reviewRestart {
		t.Fatalf("refresh key = same reviewer:%v cache:%v restart pending:%v", restarted == started, m.detailView.rawCache, m.reviewRestart)
	}

	m.detailView.rawCache["file"] = "cached diff"
	if err := os.WriteFile(file, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reload()
	if m.diffTerminal == restarted || len(m.detailView.rawCache) != 0 {
		t.Fatalf("changed worktree = same reviewer:%v cache:%v", m.diffTerminal == restarted, m.detailView.rawCache)
	}
}
