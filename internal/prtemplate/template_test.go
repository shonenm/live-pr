package prtemplate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesGitHubDefaultTemplateLocations(t *testing.T) {
	root := t.TempDir()
	if got, err := Load(root); err != nil || got != "" {
		t.Fatalf("missing template = %q, %v", got, err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "pull_request_template.md"), []byte("## Summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(root); err != nil || got != "## Summary\n" {
		t.Fatalf("template = %q, %v", got, err)
	}
}
