package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// PRSnapshot is cached Conversation data for one browsed pull request.
type PRSnapshot struct {
	PR         PR         `json:"pr"`
	Comments   []Comment  `json:"comments,omitempty"`
	Activities []Activity `json:"activities,omitempty"`
	FetchedAt  string     `json:"fetched_at,omitempty"`
}

// NavigatorCache is repository-wide browse state. Publish conflict state remains
// in the branch-local Cache and is intentionally absent here.
type NavigatorCache struct {
	Version     int                   `json:"version"`
	ViewerLogin string                `json:"viewer_login,omitempty"`
	PRs         []PR                  `json:"prs,omitempty"`
	Snapshots   map[string]PRSnapshot `json:"snapshots,omitempty"`
	FetchedAt   string                `json:"fetched_at,omitempty"`
}

func NewNavigatorCache() NavigatorCache {
	return NavigatorCache{Version: CacheVersion, Snapshots: map[string]PRSnapshot{}}
}

func (c NavigatorCache) Snapshot(number int) (PRSnapshot, bool) {
	snapshot, ok := c.Snapshots[strconv.Itoa(number)]
	return snapshot, ok
}

func (c *NavigatorCache) SetSnapshot(snapshot PRSnapshot) {
	if c.Snapshots == nil {
		c.Snapshots = map[string]PRSnapshot{}
	}
	c.Snapshots[strconv.Itoa(snapshot.PR.Number)] = snapshot
}

func LoadNavigatorCache(path string) (NavigatorCache, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewNavigatorCache(), nil
	}
	if err != nil {
		return NavigatorCache{}, err
	}
	var c NavigatorCache
	if err := json.Unmarshal(data, &c); err != nil {
		return NavigatorCache{}, fmt.Errorf("decode PR navigator cache: %w", err)
	}
	if c.Version != CacheVersion {
		return NavigatorCache{}, fmt.Errorf("unsupported PR navigator cache version %d", c.Version)
	}
	if c.Snapshots == nil {
		c.Snapshots = map[string]PRSnapshot{}
	}
	return c, nil
}

func SaveNavigatorCache(path string, c NavigatorCache) error {
	c.Version = CacheVersion
	return saveJSON(path, c)
}
