package config

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestLoadSummarizeCommand(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	if cfg := loadConfig(t, repo); cfg.SummarizeCommand != "" {
		t.Fatalf("default summarize_command = %q, want empty", cfg.SummarizeCommand)
	}
	if err := os.WriteFile(filepath.Join(repo, ".live-pr.toml"), []byte("summarize_command = 'my-agent summarize'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, repo).SummarizeCommand; got != "my-agent summarize" {
		t.Fatalf("summarize_command = %q", got)
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

func TestLoadReadsTheme(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	if got := loadConfig(t, repo).Theme; got != "" {
		t.Fatalf("default theme = %q, want empty (primer-dark)", got)
	}
	if err := os.WriteFile(filepath.Join(repo, ".live-pr.toml"), []byte("theme = \"catppuccin-mocha\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, repo).Theme; got != "catppuccin-mocha" {
		t.Fatalf("theme = %q, want catppuccin-mocha", got)
	}
}

func TestLoadMissingFilesUsesDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := Load(t.TempDir())
	if err != nil || !reflect.DeepEqual(got, Default()) {
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

func TestDefaultViewsMatchShippedTabs(t *testing.T) {
	views := Default().Views
	var names, queries []string
	for _, v := range views {
		names = append(names, v.Name)
		queries = append(queries, v.Query)
	}
	wantNames := []string{"Assigned", "Review requested", "All", "Authored", "Needs me", "Closed"}
	if strings.Join(names, "|") != strings.Join(wantNames, "|") {
		t.Fatalf("default view names = %v", names)
	}
	wantQueries := []string{"assignee:@me", "review-requested:@me", "", "author:@me", "(assignee:@me OR review-requested:@me)", "is:closed"}
	if strings.Join(queries, "|") != strings.Join(wantQueries, "|") {
		t.Fatalf("default view queries = %v", queries)
	}
	// Only the Closed tab lists closed PRs.
	for _, v := range views {
		if want := v.Name == "Closed"; v.Closed() != want {
			t.Fatalf("%s Closed() = %v", v.Name, v.Closed())
		}
	}
	if !(View{Query: "state:closed label:x"}).Closed() || (View{Query: "is:open is:closed"}).Closed() {
		t.Fatal("Closed() must read the first is:/state: token")
	}
}

func TestLoadViewsReplaceDefaultsEntirely(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	if err := os.MkdirAll(filepath.Join(global, "live-pr"), 0o755); err != nil {
		t.Fatal(err)
	}
	views := "[[views]]\nname = 'Mine'\nquery = 'author:@me'\n\n[[views]]\nname = 'Stale'\nquery = 'is:open updated:<2026-01-01'\n"
	if err := os.WriteFile(filepath.Join(global, "live-pr", "config.toml"), []byte(views), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadConfig(t, repo).Views
	if len(got) != 2 || got[0].Name != "Mine" || got[1].Name != "Stale" {
		t.Fatalf("configured views did not replace the defaults: %#v", got)
	}

	// A repo config replaces the global set in turn.
	if err := os.WriteFile(filepath.Join(repo, ".live-pr.toml"), []byte("[[views]]\nname = 'Repo'\nquery = ''\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, repo).Views; len(got) != 1 || got[0].Name != "Repo" {
		t.Fatalf("repo views = %#v", got)
	}
}

func TestNormalizeViewsDropsUnusableEntries(t *testing.T) {
	got := NormalizeViews([]View{
		{Name: "  Keep  ", Query: "  author:@me  "},
		{Name: "", Query: "orphan"},
		{Name: "keep", Query: "duplicate name, different case"},
	})
	if len(got) != 1 || got[0].Name != "Keep" || got[0].Query != "author:@me" {
		t.Fatalf("normalized = %#v", got)
	}
	if len(NormalizeViews(nil)) != len(DefaultViews()) {
		t.Fatal("an empty view set must fall back to the defaults")
	}
}
