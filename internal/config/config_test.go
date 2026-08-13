package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadConfig(t *testing.T, repo string) Config {
	t.Helper()
	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDefaultStartsBranchAndCommitCodeDiff(t *testing.T) {
	cfg := Default()
	if cfg.Diff.Command != `nvim -c "CodeDiff --inline $LIVE_PR_RANGE"` {
		t.Fatalf("branch command = %q", cfg.Diff.Command)
	}
	if cfg.Diff.CommitCommand != "" {
		t.Fatalf("built-in commit_command must remain unset, got %q", cfg.Diff.CommitCommand)
	}
	if got := cfg.CommitReviewCommand(); got != `nvim -c "CodeDiff $LIVE_PR_SHA~1 $LIVE_PR_SHA"` {
		t.Fatalf("commit command = %q", got)
	}
}

func TestCommitReviewCommandSupportsLegacyReviewer(t *testing.T) {
	cfg := Config{Reviewer: `nvim -c "CodeDiff {sha}~1 {sha}"`}
	if got := cfg.CommitReviewCommand(); got != `nvim -c "CodeDiff $LIVE_PR_SHA~1 $LIVE_PR_SHA"` {
		t.Fatalf("legacy command = %q", got)
	}
	cfg.Diff.CommitCommand = "custom"
	if got := cfg.CommitReviewCommand(); got != "custom" {
		t.Fatalf("explicit command = %q", got)
	}
}

func TestLoadAllowsExplicitlyDisablingDefaultBranchCommand(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	if err := os.WriteFile(filepath.Join(repo, ".live-pr.toml"), []byte("[diff]\ncommand = ''\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, repo).Diff.Command; got != "" {
		t.Fatalf("explicit empty command = %q", got)
	}
}

func TestLoadLegacyRepoOverride(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".live-pr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".live-pr", "config.toml"), []byte("[diff]\ncommand = ''\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, repo).Diff.Command; got != "" {
		t.Fatalf("legacy config = %q", got)
	}
}

func TestLoadDiffDisplayWithRepoOverride(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	if err := os.MkdirAll(filepath.Join(global, "live-pr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(global, "live-pr", "config.toml"), []byte("[diff]\ncommand = 'nvim branch'\ncommit_command = 'nvim commit'\ndisplay = 'cat'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".live-pr.toml"), []byte("[diff]\ndisplay = 'sed s/foo/bar/'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, repo)
	if cfg.Diff.Display != "sed s/foo/bar/" || cfg.Diff.Command != "nvim branch" || cfg.Diff.CommitCommand != "nvim commit" {
		t.Fatalf("diff config = %+v", cfg.Diff)
	}
	if cfg.Reviewer == "" {
		t.Fatal("partial config should preserve defaults")
	}
}

func TestLoadMissingFilesUsesDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := Load(t.TempDir())
	if err != nil || got != Default() {
		t.Fatalf("missing config = %+v, err=%v", got, err)
	}
}

func TestLoadReportsMalformedConfigPaths(t *testing.T) {
	for _, source := range []string{"global", "repository"} {
		t.Run(source, func(t *testing.T) {
			global := t.TempDir()
			repo := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", global)
			path := filepath.Join(repo, ".live-pr.toml")
			if source == "global" {
				path = filepath.Join(global, "live-pr", "config.toml")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("[diff\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("malformed config error = %v", err)
			}
		})
	}
}

func TestLoadReportsReadErrors(t *testing.T) {
	global := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	path := filepath.Join(global, "live-pr", "config.toml")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "read config "+path) {
		t.Fatalf("read config error = %v", err)
	}
}
