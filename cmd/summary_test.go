package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shonenm/live-pr/internal/store"
)

func TestSeedSummaryUsesRepositoryPullRequestTemplate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := "## Summary\n\n<!-- final outcome -->\n\n## Test plan\n"
	if err := os.WriteFile(filepath.Join(root, ".github", "pull_request_template.md"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.ForBranch(root, "feature")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := seedSummary(st); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(st.Conclusion())
	if err != nil || string(got) != "# <title>\n\n"+template {
		t.Fatalf("seeded summary = %q, %v", got, err)
	}
	if err := st.WriteConclusion("# Final\n\nOutcome"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(st.Conclusion())
	if strings.TrimSpace(string(got)) != "# Final\n\nOutcome" {
		t.Fatalf("updated summary = %q", got)
	}
}
