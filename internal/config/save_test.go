package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveViewsPreservesTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := `# my settings
summarize_model = "haiku"

[[views]]
name = "Old"
query = "author:@me"

[diff]
# keep this comment
command = "nvim"
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	views := []View{{Name: "Mine", Query: `author:"me"`}, {Name: "Closed", Query: "is:closed"}}
	if err := SaveViews(path, views); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"# my settings", `summarize_model = "haiku"`, "# keep this comment", `command = "nvim"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("save dropped %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `name = "Old"`) {
		t.Fatalf("stale view survived:\n%s", text)
	}
	if !strings.Contains(text, `query = "author:\"me\""`) {
		t.Fatalf("quotes not escaped:\n%s", text)
	}

	// The file still parses, and reloads as the saved views in order.
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "live-pr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(dir, "live-pr", "config.toml")); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, t.TempDir())
	if len(cfg.Views) != 2 || cfg.Views[0].Name != "Mine" || cfg.Views[1].Name != "Closed" {
		t.Fatalf("reloaded views = %#v", cfg.Views)
	}
	if cfg.SummarizeModel != "haiku" || cfg.Diff.Command != "nvim" {
		t.Fatalf("other settings changed: %+v", cfg)
	}
}

func TestSaveViewsCreatesAndAppends(t *testing.T) {
	dir := t.TempDir()
	// A missing file is created with just the views.
	fresh := filepath.Join(dir, "new", "config.toml")
	if err := SaveViews(fresh, []View{{Name: "Only", Query: ""}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "[[views]]\n") {
		t.Fatalf("new file = %q", data)
	}

	// A file without views keeps its content and gains the block.
	existing := filepath.Join(dir, "existing.toml")
	if err := os.WriteFile(existing, []byte("reviewer = \"nvim\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveViews(existing, []View{{Name: "Added", Query: "is:open"}}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(existing)
	if !strings.Contains(string(data), `reviewer = "nvim"`) || !strings.Contains(string(data), `name = "Added"`) {
		t.Fatalf("append result = %q", data)
	}
}

func TestViewsPathPrefersTheRepoFileThatOwnsViews(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	globalPath := filepath.Join(global, "live-pr", "config.toml")
	if got := ViewsPath(repo); got != globalPath {
		t.Fatalf("default target = %q", got)
	}
	repoConfig := filepath.Join(repo, ".live-pr.toml")
	if err := os.WriteFile(repoConfig, []byte("[diff]\ncommand = ''\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ViewsPath(repo); got != globalPath {
		t.Fatalf("repo file without views = %q", got)
	}
	if err := os.WriteFile(repoConfig, []byte("[[views]]\nname = 'Repo'\nquery = ''\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ViewsPath(repo); got != repoConfig {
		t.Fatalf("repo file with views = %q", got)
	}
}
