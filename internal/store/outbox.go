package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// OutboxEntry is one conversation comment queued while offline: a new post,
// or an edit of an existing GitHub comment when CommentID is set.
type OutboxEntry struct {
	ID        string `json:"id"`
	PR        int    `json:"pr"`
	Body      string `json:"body"`
	CommentID int64  `json:"comment_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

// CommentOutbox locates the queued-comment file for one pull request,
// isolated by PR number the way review drafts are so switching PRs never
// mixes queues.
func CommentOutbox(root string, number int) string {
	return filepath.Join(repoStateRoot(root), "outbox", fmt.Sprintf("%d.json", number))
}

// LoadOutbox reads the queued comments; a missing file is an empty queue.
func LoadOutbox(path string) ([]OutboxEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []OutboxEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse comment outbox %s: %w", path, err)
	}
	return entries, nil
}

// AppendOutbox queues one entry, stamping its identity and creation time,
// and returns the updated queue.
func AppendOutbox(path string, entry OutboxEntry) ([]OutboxEntry, error) {
	return mutateOutbox(path, func(entries []OutboxEntry) []OutboxEntry {
		if entry.ID == "" {
			entry.ID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(entries))
		}
		if entry.CreatedAt == "" {
			entry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		return append(entries, entry)
	})
}

// RemoveOutbox removes entries by ID and returns the updated queue. The full
// load-modify-save transaction shares AppendOutbox's process-safe file lock,
// so acknowledgements and discards cannot overwrite concurrent additions.
func RemoveOutbox(path string, ids ...string) ([]OutboxEntry, error) {
	removed := make(map[string]bool, len(ids))
	for _, id := range ids {
		removed[id] = true
	}
	return mutateOutbox(path, func(entries []OutboxEntry) []OutboxEntry {
		kept := entries[:0]
		for _, entry := range entries {
			if !removed[entry.ID] {
				kept = append(kept, entry)
			}
		}
		return kept
	})
}

func mutateOutbox(path string, mutate func([]OutboxEntry) []OutboxEntry) ([]OutboxEntry, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	unlock, err := lockOutbox(lock)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlock() }()

	entries, err := LoadOutbox(path)
	if err != nil {
		return nil, err
	}
	entries = mutate(entries)
	if err := saveOutbox(path, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// saveOutbox writes the queue atomically; the temp file carries 0600 because
// the queue holds unpublished comment text. An empty queue removes the file.
func saveOutbox(path string, entries []OutboxEntry) error {
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".outbox-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}
