package prbody

import (
	"errors"
	"strings"
	"testing"

	"github.com/shonenm/live-pr/internal/event"
)

func TestTitle(t *testing.T) {
	if got := Title("# My feature\n\nbody", "feat/x"); got != "My feature" {
		t.Errorf("Title = %q", got)
	}
	if got := Title("# <title>\n\n<current conclusion — ...>", "feat/x"); got != "feat/x" {
		t.Errorf("placeholder Title should fall back to branch, got %q", got)
	}
	if got := Title("# <title>\n\n## Summary\n\nOutcome", "feat/x"); got != "feat/x" {
		t.Errorf("unfilled PR template should fall back to branch, got %q", got)
	}
}

func TestRenderPutsConclusionAboveTimeline(t *testing.T) {
	events := []event.Event{
		{TS: "2026-07-21T10:05", Kind: event.Decision, Title: "chose Go", Body: "- gh-dash stack\n- single binary"},
		{TS: "2026-07-21T11:10", Kind: event.Commit, Title: "feat: tui", SHA: "ba3c635"},
	}
	out := Render("# Feature\n\nThe final conclusion.", events)

	concl := strings.Index(out, "The final conclusion.")
	tl := strings.Index(out, "## Development timeline")
	if concl < 0 || tl < 0 || concl > tl {
		t.Fatalf("conclusion must appear above the timeline\n%s", out)
	}
	for _, want := range []string{
		"- **decision** (2026-07-21T10:05) — chose Go",
		"  - gh-dash stack", // body indented under the item
		"- **commit** `ba3c635` — feat: tui",
		"captured with [live-pr]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderOmitsPlaceholderConclusion(t *testing.T) {
	out := Render("# <title>\n\n<current conclusion — overwrite this>", nil)
	if strings.Contains(out, "<current conclusion") {
		t.Errorf("placeholder conclusion should be omitted:\n%s", out)
	}
	if !strings.Contains(out, "_No timeline events yet._") {
		t.Errorf("empty timeline should say so:\n%s", out)
	}
}

func TestRenderWrapsManagedBlock(t *testing.T) {
	out := Render("# Feature", nil)
	if !strings.HasPrefix(out, ManagedStart+"\n") || !strings.Contains(out, ManagedEnd) {
		t.Fatalf("missing managed markers:\n%s", out)
	}
	if ManagedHash(out) == "" {
		t.Fatal("managed block should have a hash")
	}
}

func TestMergePreservesOutsideAndDetectsConflict(t *testing.T) {
	old := Render("# Old", nil)
	remote := "human intro\n\n" + old + "\nhuman footer\n"
	next := Render("# New", nil)

	merged, err := Merge(remote, next, ManagedHash(remote))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"human intro", "# New", "human footer"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("merged body missing %q:\n%s", want, merged)
		}
	}
	if strings.Contains(merged, "# Old") {
		t.Fatalf("old managed body remained:\n%s", merged)
	}

	if _, err := Merge(remote, next, Hash("different")); !errors.Is(err, ErrManagedConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestMergeRequiresBaselineForExistingOrDeletedManagedBlock(t *testing.T) {
	old := Render("# Old", nil)
	next := Render("# New", nil)
	if _, err := Merge(old, next, ""); !errors.Is(err, ErrManagedConflict) {
		t.Fatalf("expected missing-baseline conflict, got %v", err)
	}
	if _, err := Merge("human body after managed block was deleted", next, ManagedHash(old)); !errors.Is(err, ErrManagedConflict) {
		t.Fatalf("expected deleted-block conflict, got %v", err)
	}
}

func TestMergeAppendsToUnmanagedBody(t *testing.T) {
	next := Render("# Feature", nil)
	merged, err := Merge("human-owned body", next, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(merged, "human-owned body") || !strings.Contains(merged, ManagedStart) {
		t.Fatalf("unexpected merge:\n%s", merged)
	}
}
