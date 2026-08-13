// Package prbody renders a pull-request body from the conclusion and timeline.
package prbody

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/shonenm/live-pr/internal/event"
)

const (
	ManagedStart = "<!-- live-pr:managed:start v=1 -->"
	ManagedEnd   = "<!-- live-pr:managed:end -->"
)

var (
	// ErrManagedConflict means the live-pr-owned block changed since it was fetched.
	ErrManagedConflict = errors.New("managed PR body changed on GitHub")
	// ErrMalformedManagedBlock means marker ownership cannot be determined safely.
	ErrMalformedManagedBlock = errors.New("malformed live-pr managed block")
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
	for _, raw := range strings.Split(conclusion, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "<!--") {
			continue
		}
		if strings.HasPrefix(line, "##") {
			return branch
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line == "" || line == "<title>" || strings.HasPrefix(line, "<current conclusion") || strings.HasPrefix(line, "<final pull request summary") {
			continue
		}
		return line
	}
	return branch
}

// Render assembles the PR body: the conclusion (head) on top, then the
// chronological development timeline below it.
func Render(conclusion string, events []event.Event) string {
	var b strings.Builder
	b.WriteString(ManagedStart)
	b.WriteString("\n")

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
	b.WriteString(ManagedEnd)
	b.WriteString("\n")
	return b.String()
}

// Merge replaces only live-pr's managed block. Text outside the block is
// preserved. An unmarked remote body is preserved and the block is appended.
// expectedHash must match the fetched managed block before it can be replaced.
func Merge(remote, next, expectedHash string) (string, error) {
	_, _, _, err := managedRange(next)
	if err != nil {
		return "", err
	}
	start, end, current, err := managedRange(remote)
	if errors.Is(err, errNoManagedBlock) {
		if expectedHash != "" {
			return "", ErrManagedConflict
		}
		if strings.TrimSpace(remote) == "" {
			return next, nil
		}
		return strings.TrimRight(remote, "\n") + "\n\n" + next, nil
	}
	if err != nil {
		return "", err
	}
	if expectedHash == "" || Hash(current) != expectedHash {
		if current == strings.TrimSpace(next) {
			return remote, nil
		}
		return "", ErrManagedConflict
	}
	return remote[:start] + strings.TrimSpace(next) + remote[end:], nil
}

// ManagedHash returns the hash of the managed block, or an empty string when
// no valid block exists.
func ManagedHash(body string) string {
	_, _, block, err := managedRange(body)
	if err != nil {
		return ""
	}
	return Hash(block)
}

// Hash returns a stable content hash used for optimistic conflict detection.
func Hash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(s)))) }

var errNoManagedBlock = errors.New("no managed block")

func managedRange(body string) (start, end int, block string, err error) {
	start = strings.Index(body, ManagedStart)
	endMarker := strings.Index(body, ManagedEnd)
	if start < 0 && endMarker < 0 {
		return 0, 0, "", errNoManagedBlock
	}
	if start < 0 || endMarker < start || strings.Count(body, ManagedStart) != 1 || strings.Count(body, ManagedEnd) != 1 {
		return 0, 0, "", ErrMalformedManagedBlock
	}
	end = endMarker + len(ManagedEnd)
	return start, end, strings.TrimSpace(body[start:end]), nil
}

func writeEvent(b *strings.Builder, e event.Event) {
	meta := "(" + e.TS
	if e.Author != "" {
		meta += " · " + e.Author
	}
	meta += ")"
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
	return strings.Contains(c, "<current conclusion") || strings.Contains(c, "<final pull request summary") || strings.Contains(c, "# <title>")
}
