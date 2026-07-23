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
	dir := filepath.Join(root, ".live-pr", slug(branch))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{Root: root, Branch: branch, Dir: dir}, nil
}

// Timeline is the path to the append-only event log.
func (s *Store) Timeline() string { return filepath.Join(s.Dir, "timeline.jsonl") }

// Conclusion is the path to the pinned head document.
func (s *Store) Conclusion() string { return filepath.Join(s.Dir, "conclusion.md") }
