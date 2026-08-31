package store

import (
	"errors"

	gh "github.com/shonenm/live-pr/internal/github"
)

// LoadGitHubCache loads this branch's GitHub cache from its canonical path.
func (s *Store) LoadGitHubCache() (gh.Cache, error) {
	return gh.LoadCache(s.GitHubCache(), s.Branch)
}

// LoadSession resolves the current branch's workspace: the discovered store
// plus its GitHub cache, which carries the resolved PR when one is known.
// Malformed cache data is re-fetchable, so it becomes an empty cache carrying
// a warning; filesystem errors remain fatal.
func LoadSession() (*Store, gh.Cache, error) {
	st, err := Discover()
	if err != nil {
		return nil, gh.Cache{}, err
	}
	cache, err := st.LoadGitHubCache()
	if errors.Is(err, gh.ErrInvalidCache) {
		cache = gh.NewCache(st.Branch)
		cache.Warning = err.Error()
	} else if err != nil {
		return nil, gh.Cache{}, err
	}
	return st, cache, nil
}
