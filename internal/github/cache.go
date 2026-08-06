package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const CacheVersion = 1

// Cache is branch-local mutable GitHub state. Remote resources intentionally
// stay separate from the append-only local timeline.
type Cache struct {
	Version                      int        `json:"version"`
	Head                         string     `json:"head,omitempty"`
	PR                           *PR        `json:"pr,omitempty"`
	Comments                     []Comment  `json:"comments,omitempty"`
	Activities                   []Activity `json:"activities,omitempty"`
	FetchedAt                    string     `json:"fetched_at,omitempty"`
	LastPublishedManagedBodyHash string     `json:"last_published_managed_body_hash,omitempty"`
}

// NewCache returns an initialized empty cache.
func NewCache(head string) Cache { return Cache{Version: CacheVersion, Head: head} }

// LoadCache loads path. A missing file is an empty cache, while malformed or
// unsupported data is reported so callers can keep running without trusting it.
func LoadCache(path, head string) (Cache, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewCache(head), nil
	}
	if err != nil {
		return Cache{}, err
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return Cache{}, fmt.Errorf("decode GitHub cache: %w", err)
	}
	if c.Version != CacheVersion {
		return Cache{}, fmt.Errorf("unsupported GitHub cache version %d", c.Version)
	}
	if c.Head != "" && c.Head != head {
		return NewCache(head), nil
	}
	c.Head = head
	return c, nil
}

// SaveCache atomically replaces path with c.
func SaveCache(path string, c Cache) error {
	c.Version = CacheVersion
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".github-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
