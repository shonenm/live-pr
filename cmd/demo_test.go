package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCreateDemoRepo(t *testing.T) {
	root := t.TempDir()
	if err := createDemoRepo(root, "delta"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "log", "--oneline", "main..HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(out)), "\n")); got != 2 {
		t.Fatalf("demo commits = %d, want 2", got)
	}
	config, err := os.ReadFile(root + "/.live-pr.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `display = "delta --color-only --paging=never --line-numbers"`) {
		t.Fatalf("demo config = %s", config)
	}
}
