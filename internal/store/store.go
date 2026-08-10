// Package store locates live-pr's user-level runtime data for one repo + branch.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shonenm/live-pr/internal/git"
)

// Layout lives outside the repository:
//
//	$XDG_STATE_HOME/live-pr/repos/<repo>/<branch>/
//	  timeline.jsonl
//	  conclusion.md
//
// The repository path is hashed so two repositories with the same directory
// name cannot share state.
type Store struct {
	Root   string // repo top level
	Branch string // current branch
	Dir    string // user-level state directory for this repo + branch
}

// Discover resolves the store for the current repo and branch, creating the
// per-branch directory if needed.
func Discover() (*Store, error) {
	root, err := git.RepoRoot()
	if err != nil {
		return nil, err
	}
	if err := MigrateLegacy(root); err != nil {
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
	return &Store{Root: root, Branch: branch, Dir: filepath.Join(repoStateRoot(root), slug(branch))}
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

// Timeline is the path to the append-only local event log.
func (s *Store) Timeline() string { return filepath.Join(s.Dir, "timeline.jsonl") }

// Conclusion is the path to the pinned current conclusion.
func (s *Store) Conclusion() string { return filepath.Join(s.Dir, "conclusion.md") }

// GitHubCache is the path to mutable remote state kept separate from the timeline.
func (s *Store) GitHubCache() string { return filepath.Join(s.Dir, "github.json") }

// NavigatorCache is the repository-wide PR list and remote snapshot cache.
func NavigatorCache(root string) string { return filepath.Join(repoStateRoot(root), "github-prs.json") }

// MigrateLegacy moves data from the old repository-local .live-pr/ layout.
// Legacy config.toml is renamed to .live-pr.toml when possible; the old path
// remains readable through config.Load if migration cannot move it.
func MigrateLegacy(root string) error {
	legacy := filepath.Join(root, ".live-pr")
	entries, err := os.ReadDir(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	state := repoStateRoot(root)
	legacyConfig := filepath.Join(legacy, "config.toml")
	newConfig := filepath.Join(root, ".live-pr.toml")
	if _, err := os.Stat(legacyConfig); err == nil {
		if _, targetErr := os.Stat(newConfig); errors.Is(targetErr, os.ErrNotExist) {
			if err := os.Rename(legacyConfig, newConfig); err != nil {
				return err
			}
		}
	}
	for _, entry := range entries {
		if entry.Name() == "config.toml" {
			continue
		}
		var target string
		if entry.Name() == "github-prs.json" {
			target = NavigatorCache(root)
		} else if entry.IsDir() {
			target = filepath.Join(state, entry.Name())
		} else {
			continue
		}
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(legacy, entry.Name()), target); err != nil {
			return err
		}
	}
	_ = os.Remove(legacy)
	return nil
}

func repoStateRoot(root string) string {
	clean, err := filepath.Abs(root)
	if err != nil {
		clean = root
	}
	hash := sha256.Sum256([]byte(clean))
	name := filepath.Base(filepath.Clean(clean))
	return filepath.Join(stateRoot(), "repos", slug(name)+"-"+hex.EncodeToString(hash[:])[:12])
}

func stateRoot() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "live-pr")
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, "Library", "Application Support", "live-pr")
		}
		if runtime.GOOS != "windows" {
			return filepath.Join(home, ".local", "state", "live-pr")
		}
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "live-pr", "state")
	}
	return filepath.Join(os.TempDir(), "live-pr")
}

func slug(branch string) string {
	return strings.NewReplacer("/", "-", " ", "-", ":", "-").Replace(branch)
}
