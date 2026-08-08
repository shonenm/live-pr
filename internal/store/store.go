// Package store locates the per-branch live-pr data directory and the files in it.
//
// Layout, rooted at the repo top level:
//
//	.live-pr/<branch-slug>/
//	  timeline.jsonl   append-only event log
//	  conclusion.md    the pinned "current conclusion" (head), overwritten in place
package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shonenm/live-pr/internal/git"
)

// Store is the resolved live-pr data location for one repo + branch.
type Store struct {
	Root   string // repo top level
	Branch string // current branch
	Dir    string // .live-pr/<branch-slug>
}

// slug makes a branch name safe as a single path segment.
func slug(branch string) string {
	return strings.NewReplacer("/", "-", " ", "-", ":", "-").Replace(branch)
}

// Discover resolves the store for the current repo and branch, creating the
// per-branch directory if needed.
func Discover() (*Store, error) {
	root, err := git.RepoRoot()
	if err != nil {
		return nil, err
	}
	branch, err := git.CurrentBranch()
	if err != nil {
		return nil, err
	}
	st := ForBranch(root, branch)
	if err := os.MkdirAll(st.Dir, 0o755); err != nil {
		return nil, err
	}
	return st, nil
}

// ForBranch resolves branch paths without creating files or directories.
func ForBranch(root, branch string) *Store {
	return &Store{Root: root, Branch: branch, Dir: filepath.Join(root, ".live-pr", slug(branch))}
}

// Ensure creates the branch data directory.
func (s *Store) Ensure() error { return os.MkdirAll(s.Dir, 0o755) }

// HasData reports whether a branch has meaningful local PR content.
func (s *Store) HasData() bool {
	for _, path := range []string{s.Timeline(), s.Conclusion()} {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return true
		}
	}
	return false
}

// Timeline is the path to the append-only event log.
func (s *Store) Timeline() string { return filepath.Join(s.Dir, "timeline.jsonl") }

// Conclusion is the path to the pinned head document.
func (s *Store) Conclusion() string { return filepath.Join(s.Dir, "conclusion.md") }

// GitHubCache is the path to mutable remote state kept separate from timeline.jsonl.
func (s *Store) GitHubCache() string { return filepath.Join(s.Dir, "github.json") }

// NavigatorCache is the repository-wide PR list and remote snapshot cache.
func NavigatorCache(root string) string { return filepath.Join(root, ".live-pr", "github-prs.json") }
