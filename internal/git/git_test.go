package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
}
