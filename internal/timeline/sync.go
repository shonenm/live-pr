// Package timeline holds higher-level operations over the event log.
package timeline

import (
	"strings"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
)

// WithCommits returns events plus every commit not already represented, without
// writing the timeline. New commit events are appended in commit order.
func WithCommits(events []event.Event, commits []git.Commit) []event.Event {
	merged := append([]event.Event(nil), events...)
	seen := make(map[string]bool)
	for _, e := range events {
		if e.Kind == event.Commit && e.SHA != "" {
			seen[e.SHA] = true
		}
	}
	for _, c := range commits {
		if seen[c.SHA] {
			continue
		}
		merged = append(merged, event.Event{
			TS:    c.Date,
			Kind:  event.Commit,
			Title: c.Subject,
			Body:  strings.TrimSpace(c.Body),
			SHA:   c.SHA,
		})
		seen[c.SHA] = true
	}
	return merged
}

// SyncCommits appends a commit event for every base..HEAD commit not already on
// the timeline, in commit order (oldest first). It is idempotent: commits whose
// sha is already present are skipped, so it is safe to run on every TUI open.
// Returns the number of new events appended.
func SyncCommits(path, base string) (int, error) {
	existing, err := event.Load(path)
	if err != nil {
		return 0, err
	}
	commits, err := git.Commits(base)
	if err != nil {
		return 0, err
	}
	merged := WithCommits(existing, commits)
	added := 0
	for _, ev := range merged[len(existing):] {
		if err := event.Append(path, ev); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}
