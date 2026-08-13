package prtemplate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shonenm/live-pr/internal/store"
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

func TestSeed(t *testing.T) {
	newStore := func(t *testing.T) *store.Store {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		return store.ForBranch(t.TempDir(), "feature")
	}

	t.Run("existing conclusion is left untouched", func(t *testing.T) {
		st := newStore(t)
		if err := st.WriteConclusion("hand written"); err != nil {
			t.Fatal(err)
		}
		if err := Seed(st); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(st.Conclusion())
		if string(got) != "hand written\n" {
			t.Fatalf("Seed overwrote existing conclusion: %q", got)
		}
	})

	t.Run("no template writes the default summary", func(t *testing.T) {
		st := newStore(t)
		if err := Seed(st); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(st.Conclusion())
		if string(got) != defaultSummary {
			t.Fatalf("Seed = %q, want defaultSummary", got)
		}
	})

	t.Run("template is wrapped under a title", func(t *testing.T) {
		st := newStore(t)
		if err := os.MkdirAll(filepath.Join(st.Root, ".github"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(st.Root, ".github", "pull_request_template.md"), []byte("## Summary\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Seed(st); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(st.Conclusion())
		if want := "# <title>\n\n## Summary\n"; string(got) != want {
			t.Fatalf("Seed = %q, want %q", got, want)
		}
	})
}
