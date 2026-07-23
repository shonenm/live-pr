// Package timeline holds higher-level operations over the event log.
package timeline

import (
	"strings"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
)

// SyncCommits appends a commit event for every base..HEAD commit not already on
// the timeline, in commit order (oldest first). It is idempotent: commits whose
// sha is already present are skipped, so it is safe to run on every TUI open.
// Returns the number of new events appended.
func SyncCommits(path, base string) (int, error) {
	existing, err := event.Load(path)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]bool)
	for _, e := range existing {
		if e.Kind == event.Commit && e.SHA != "" {
			seen[e.SHA] = true
		}
	}

	commits, err := git.Commits(base)
	if err != nil {
		return 0, err
	}

	added := 0
	for _, c := range commits {
		if seen[c.SHA] {
			continue
		}
		ev := event.Event{
			TS:    c.Date,
			Kind:  event.Commit,
			Title: c.Subject,
			Body:  strings.TrimSpace(c.Body),
			SHA:   c.SHA,
		}
		if err := event.Append(path, ev); err != nil {
			return added, err
		}
		seen[c.SHA] = true
		added++
	}
	return added, nil
}
