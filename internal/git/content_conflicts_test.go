package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// countGitCommands puts a logging shim in front of git for the rest of the
// test and returns a function reporting how often each subcommand has run
// since.
func countGitCommands(t *testing.T) func() map[string]int {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	logPath := filepath.Join(shimDir, "log")
	script := "#!/bin/sh\necho \"$1\" >> '" + logPath + "'\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() map[string]int {
		t.Helper()
		counts := map[string]int{}
		data, err := os.ReadFile(logPath)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, name := range strings.Fields(string(data)) {
			counts[name]++
		}
		return counts
	}
}

func TestContentConflictsSpawnsFewGitProcesses(t *testing.T) {
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
	body := func(first, last string) string {
		return first + "\n" + strings.Repeat("filler\n", 8) + last + "\n"
	}
	const diverged, same = 5, 8
	for i := range diverged {
		write(fmt.Sprintf("diverged-%d.txt", i), body("top", "bottom"))
	}
	for i := range same {
		write(fmt.Sprintf("same-%d.txt", i), "shared\n")
	}
	write("conflict.txt", "shared\n")
	write("mode.sh", "shared\n")
	run("add", ".")
	run("commit", "-m", "base")

	run("switch", "-c", "feature")
	for i := range diverged {
		write(fmt.Sprintf("diverged-%d.txt", i), body("ours-top", "bottom"))
	}
	for i := range same {
		write(fmt.Sprintf("same-%d.txt", i), "picked\n")
	}
	write("conflict.txt", "ours\n")
	run("update-index", "--chmod=+x", "mode.sh")
	run("commit", "-am", "ours")

	run("switch", "main")
	for i := range diverged {
		write(fmt.Sprintf("diverged-%d.txt", i), body("top", "theirs-bottom"))
	}
	for i := range same {
		write(fmt.Sprintf("same-%d.txt", i), "picked\n")
	}
	write("conflict.txt", "theirs\n")
	write("mode.sh", "theirs\n")
	run("commit", "-am", "theirs")

	counts := countGitCommands(t)
	files, err := contentConflicts("main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"conflict.txt"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("contentConflicts = %#v, want %#v", files, want)
	}
	// Only the paths whose content really diverged need a three-way merge;
	// the same-N files and the mode-only change are settled from blob IDs.
	// All blobs come from a single cat-file process.
	want := map[string]int{"merge-base": 1, "diff": 2, "cat-file": 1, "merge-file": diverged + 1}
	if got := counts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("git commands = %v, want %v", got, want)
	}
}

func TestContentConflictsWithoutDivergedContentReadsNoBlobs(t *testing.T) {
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
	write("same.txt", "shared\n")
	write("modify-delete.txt", "shared\n")
	write("delete-delete.txt", "shared\n")
	run("add", ".")
	run("commit", "-m", "base")

	run("switch", "-c", "feature")
	write("same.txt", "picked\n")
	write("modify-delete.txt", "ours\n")
	run("rm", "delete-delete.txt")
	run("commit", "-am", "ours")

	run("switch", "main")
	write("same.txt", "picked\n")
	run("rm", "modify-delete.txt")
	run("rm", "delete-delete.txt")
	run("commit", "-am", "theirs")

	counts := countGitCommands(t)
	files, err := contentConflicts("main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"modify-delete.txt"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("contentConflicts = %#v, want %#v", files, want)
	}
	want := map[string]int{"merge-base": 1, "diff": 2}
	if got := counts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("git commands = %v, want %v", got, want)
	}
}

// A rename record carries two paths and the source's blob ID, so the renamed
// file must be keyed by its new name and merged against an empty ancestor.
func TestContentConflictsRenameOntoAddedPath(t *testing.T) {
	run := gitRepo(t)
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "diff.renames", "true")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	original := strings.Repeat("line\n", 20)
	write("old name.txt", original)
	write("tail.txt", "shared\n")
	run("add", ".")
	run("commit", "-m", "base")

	run("switch", "-c", "feature")
	run("mv", "old name.txt", "new name.txt")
	write("new name.txt", original+"ours\n")
	write("tail.txt", "ours\n")
	run("add", ".")
	run("commit", "-m", "ours")

	run("switch", "main")
	write("new name.txt", "theirs\n")
	write("tail.txt", "theirs\n")
	run("add", ".")
	run("commit", "-m", "theirs")

	files, err := contentConflicts("main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"new name.txt", "tail.txt"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("contentConflicts = %#v, want %#v", files, want)
	}
}
