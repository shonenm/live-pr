package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --diff has to reach the first embedded reviewer and every Model rebuilt
// after a checkout or branch switch, not just the Model Run starts with.
func TestDiffOptionAppliesBeforeHydrationAndSurvivesRebuild(t *testing.T) {
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
	for _, branch := range []string{"feature-a", "feature-b"} {
		run("switch", "-c", branch, "main")
		if err := os.WriteFile(file, []byte(branch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("commit", "-am", branch)
	}
	run("switch", "feature-a")
	t.Chdir(root)

	check := func(name string, m Model) {
		t.Helper()
		if m.screen != detailScreen || m.diffCommand != "" || m.diffCommitCommand != "" || m.diffDisplay != "" || m.diffTerminal != nil {
			t.Fatalf("%s = screen:%v command:%q commit:%q display:%q terminal:%v", name, m.screen, m.diffCommand, m.diffCommitCommand, m.diffDisplay, m.diffTerminal != nil)
		}
	}
	old, err := New("test", WithDiff("git"))
	if err != nil {
		t.Fatal(err)
	}
	defer old.close()
	// The default reviewer is nvim; "git" must win before the terminal is built.
	check("startup", old)

	run("switch", "feature-b")
	switched := rebuildForLocalBranchChange(old.version, old.options, old.localGeneration)().(localBranchReloaded)
	if switched.err != nil || switched.next == nil {
		t.Fatalf("branch rebuild = next:%v err:%v", switched.next, switched.err)
	}
	defer switched.next.close()
	check("branch rebuild", *switched.next)

	reloaded := rebuildAfterCheckout(old.version, old.options, old.targetGeneration, prActionDone{})().(checkoutReloaded)
	if reloaded.err != nil || reloaded.next == nil {
		t.Fatalf("checkout rebuild = next:%v err:%v", reloaded.next, reloaded.err)
	}
	defer reloaded.next.close()
	check("checkout rebuild", *reloaded.next)
}
