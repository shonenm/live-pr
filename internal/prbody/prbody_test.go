package prbody

import (
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
