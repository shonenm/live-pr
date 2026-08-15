package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/shonenm/live-pr/internal/debugtime"
)

const maxNavigatorSnapshots = 50

// PRSnapshot is cached Conversation data for one browsed pull request.
type PRSnapshot struct {
	PR         PR         `json:"pr"`
	Comments   []Comment  `json:"comments,omitempty"`
	Activities []Activity `json:"activities,omitempty"`
	FetchedAt  string     `json:"fetched_at,omitempty"`
}

// PRViewCache preserves loaded row order and an exact server count without
// duplicating PR metadata already stored in NavigatorCache.PRs.
type PRViewCache struct {
	Numbers    []int  `json:"numbers,omitempty"`
	TotalCount int    `json:"total_count,omitempty"`
	FetchedAt  string `json:"fetched_at,omitempty"`
}

// NavigatorCache is repository-wide browse state. Publish conflict state remains
// in the branch-local Cache and is intentionally absent here.
type NavigatorCache struct {
	Version       int                    `json:"version"`
	Repository    string                 `json:"repository,omitempty"`
	ViewerLogin   string                 `json:"viewer_login,omitempty"`
	PRs           []PR                   `json:"prs,omitempty"`
	PRsState      string                 `json:"prs_state,omitempty"`
	FetchedStates map[string]bool        `json:"fetched_states,omitempty"`
	Views         map[string]PRViewCache `json:"views,omitempty"`
	Snapshots     map[string]PRSnapshot  `json:"snapshots,omitempty"`
	FetchedAt     string                 `json:"fetched_at,omitempty"`
}

func NewNavigatorCache() NavigatorCache {
	return NavigatorCache{Version: CacheVersion, PRsState: "OPEN", FetchedStates: map[string]bool{}, Views: map[string]PRViewCache{}, Snapshots: map[string]PRSnapshot{}}
}

func (c NavigatorCache) View(name string) ([]PR, PRViewCache, bool) {
	view, ok := c.Views[name]
	if !ok {
		return nil, PRViewCache{}, false
	}
	byNumber := make(map[int]PR, len(c.PRs))
	for _, pr := range c.PRs {
		byNumber[pr.Number] = pr
	}
	prs := make([]PR, 0, len(view.Numbers))
	for _, number := range view.Numbers {
		if pr, exists := byNumber[number]; exists {
			prs = append(prs, pr)
		}
	}
	return prs, view, true
}

func (c *NavigatorCache) SetView(name string, prs []PR, total int, fetchedAt string) {
	if c.Views == nil {
		c.Views = map[string]PRViewCache{}
	}
	numbers := make([]int, 0, len(prs))
	for _, pr := range prs {
		if pr.Number > 0 {
			numbers = append(numbers, pr.Number)
		}
	}
	c.Views[name] = PRViewCache{Numbers: numbers, TotalCount: total, FetchedAt: fetchedAt}
}

func (c *NavigatorCache) PrunePRs() {
	if len(c.Views) == 0 {
		return
	}
	keep := map[int]bool{}
	for _, view := range c.Views {
		for _, number := range view.Numbers {
			keep[number] = true
		}
	}
	prs := c.PRs[:0]
	for _, pr := range c.PRs {
		if keep[pr.Number] {
			prs = append(prs, pr)
		}
	}
	c.PRs = prs
}

func (c NavigatorCache) Snapshot(number int) (PRSnapshot, bool) {
	snapshot, ok := c.Snapshots[strconv.Itoa(number)]
	return snapshot, ok
}

func (c *NavigatorCache) SetSnapshot(snapshot PRSnapshot) {
	if c.Snapshots == nil {
		c.Snapshots = map[string]PRSnapshot{}
	}
	number := snapshot.PR.Number
	// Detailed PR metadata already lives in PRs; snapshots only add Conversation data.
	snapshot.PR = PR{Number: number}
	c.Snapshots[strconv.Itoa(number)] = snapshot
	c.trimSnapshots()
}

func (c *NavigatorCache) trimSnapshots() {
	if len(c.Snapshots) <= maxNavigatorSnapshots {
		return
	}
	keys := make([]string, 0, len(c.Snapshots))
	for key := range c.Snapshots {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := c.Snapshots[keys[i]], c.Snapshots[keys[j]]
		if left.FetchedAt == right.FetchedAt {
			return keys[i] < keys[j]
		}
		return left.FetchedAt < right.FetchedAt
	})
	for _, key := range keys[:len(keys)-maxNavigatorSnapshots] {
		delete(c.Snapshots, key)
	}
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
	if c.PRsState == "" {
		// Older caches only contained the default open-PR list.
		c.PRsState = "OPEN"
	}
	if c.FetchedStates == nil {
		c.FetchedStates = map[string]bool{c.PRsState: true}
	}
	if c.Views == nil {
		c.Views = map[string]PRViewCache{}
	}
	if c.Snapshots == nil {
		c.Snapshots = map[string]PRSnapshot{}
	}
	c.trimSnapshots()
	return c, nil
}

// Clone returns a copy that is safe to marshal while the original keeps
// mutating: the PRs backing array is rewritten in place by preview updates and
// the maps by SetView/SetSnapshot, but their values are only ever replaced
// wholesale, so element copies suffice.
func (c NavigatorCache) Clone() NavigatorCache {
	c.PRs = append([]PR(nil), c.PRs...)
	states := make(map[string]bool, len(c.FetchedStates))
	for k, v := range c.FetchedStates {
		states[k] = v
	}
	c.FetchedStates = states
	views := make(map[string]PRViewCache, len(c.Views))
	for k, v := range c.Views {
		views[k] = v
	}
	c.Views = views
	snapshots := make(map[string]PRSnapshot, len(c.Snapshots))
	for k, v := range c.Snapshots {
		snapshots[k] = v
	}
	c.Snapshots = snapshots
	return c
}

func SaveNavigatorCache(path string, c NavigatorCache) error {
	if done := debugtime.Start("navigator cache save"); done != nil {
		defer done()
	}
	c.Version = CacheVersion
	return saveJSON(path, c)
}
