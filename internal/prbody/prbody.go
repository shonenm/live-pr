// Package prbody renders a pull-request body from the conclusion and timeline.
package prbody

import (
	"fmt"
	"strings"

	"github.com/shonenm/live-pr/internal/event"
)

func label(k event.Kind) string {
	switch k {
	case event.Decision:
		return "decision"
	case event.Pivot:
		return "pivot"
	case event.Commit:
		return "commit"
	case event.Summary:
		return "summary"
	default:
		return "note"
	}
}

// Title is the PR title: the first meaningful line of the conclusion (skipping
// the seeded placeholders), else the branch name.
func Title(conclusion, branch string) string {
	for _, ln := range strings.Split(conclusion, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(ln, "# "))
		if ln == "" || ln == "<title>" || strings.HasPrefix(ln, "<current conclusion") {
			continue
		}
		return ln
	}
	return branch
}

// Render assembles the PR body: the conclusion (head) on top, then the
// chronological development timeline below it.
func Render(conclusion string, events []event.Event) string {
	var b strings.Builder

	if c := strings.TrimSpace(conclusion); c != "" && !isPlaceholder(c) {
		b.WriteString(c)
		b.WriteString("\n\n")
	}

	b.WriteString("## Development timeline\n\n")
	if len(events) == 0 {
		b.WriteString("_No timeline events yet._\n")
	} else {
		for _, e := range events {
			writeEvent(&b, e)
		}
	}

	b.WriteString("\n---\n_Decision timeline captured with [live-pr](https://github.com/shonenm/live-pr)._\n")
	return b.String()
}

func writeEvent(b *strings.Builder, e event.Event) {
	meta := "(" + e.TS + ")"
	if e.Kind == event.Commit && e.SHA != "" {
		meta = "`" + e.SHA + "`"
	}
	fmt.Fprintf(b, "- **%s** %s — %s\n", label(e.Kind), meta, e.Title)
	if body := strings.TrimSpace(e.Body); body != "" {
		for _, ln := range strings.Split(body, "\n") {
			fmt.Fprintf(b, "  %s\n", ln) // indent to stay within the list item
		}
	}
}

func isPlaceholder(c string) bool {
	return strings.Contains(c, "<current conclusion") || strings.Contains(c, "# <title>")
}
