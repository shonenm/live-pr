package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDiffDisplayWithRepoOverride(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	if err := os.MkdirAll(filepath.Join(global, "live-pr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(global, "live-pr", "config.toml"), []byte("[diff]\ncommand = 'nvim'\ndisplay = 'cat'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".live-pr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".live-pr", "config.toml"), []byte("[diff]\ndisplay = 'sed s/foo/bar/'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(repo)
	if cfg.Diff.Display != "sed s/foo/bar/" || cfg.Diff.Command != "nvim" {
		t.Fatalf("diff config = %+v", cfg.Diff)
	}
	if cfg.Reviewer == "" {
		t.Fatal("partial config should preserve defaults")
	}
}
