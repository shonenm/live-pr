// Package timeline holds higher-level operations over the event log.
package timeline

import (
	"slices"
	"strings"

	"github.com/shonenm/live-pr/v2/internal/event"
	"github.com/shonenm/live-pr/v2/internal/filelock"
	"github.com/shonenm/live-pr/v2/internal/git"
)

// WithCommits returns events plus every commit not already represented, without
// writing the timeline. New commit events are appended in commit order.
func WithCommits(events []event.Event, commits []git.Commit) []event.Event {
	merged := append([]event.Event(nil), events...)
	var seen []string
	for _, e := range events {
		if e.Kind == event.Commit && e.SHA != "" {
			seen = append(seen, e.SHA)
		}
	}
	for _, c := range commits {
		if slices.ContainsFunc(seen, func(sha string) bool { return sameCommit(sha, c.SHA) }) {
			continue
		}
		merged = append(merged, event.Event{
			TS:    c.Date,
			Kind:  event.Commit,
			Title: c.Subject,
			Body:  strings.TrimSpace(c.Body),
			SHA:   c.SHA,
		})
		seen = append(seen, c.SHA)
	}
	return merged
}

// sameCommit reports whether two abbreviated shas name the same commit. Git's
// abbreviation length grows with the repository (and follows core.abbrev), so
// a sha recorded earlier may be shorter or longer than the one git prints now.
func sameCommit(a, b string) bool {
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// SyncCommits appends a commit event for every base..HEAD commit not already on
// the timeline, in commit order (oldest first). It is idempotent: commits whose
// sha is already present are skipped, so it is safe to run on every TUI open.
// Returns the number of new events appended.
func SyncCommits(path, base string) (int, error) {
	commits, err := git.Commits(base)
	if err != nil {
		return 0, err
	}
	unlock, err := filelock.Acquire(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unlock() }()
	existing, err := event.Load(path)
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
