package store

import (
	gh "github.com/shonenm/live-pr/internal/github"
)

// LoadGitHubCache loads this branch's GitHub cache from its canonical path.
func (s *Store) LoadGitHubCache() (gh.Cache, error) {
	return gh.LoadCache(s.GitHubCache(), s.Branch)
}

// LoadSession resolves the current branch's workspace: the discovered store
// plus its GitHub cache, which carries the resolved PR when one is known
// (Cache.PR). It fails when the cache exists but cannot be read; callers that
// tolerate a broken cache load it themselves via LoadGitHubCache.
func LoadSession() (*Store, gh.Cache, error) {
	st, err := Discover()
	if err != nil {
		return nil, gh.Cache{}, err
	}
	cache, err := st.LoadGitHubCache()
	if err != nil {
		return nil, gh.Cache{}, err
	}
	return st, cache, nil
}
