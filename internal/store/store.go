// Package store locates live-pr's user-level runtime data for one repo + branch.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

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
	if err := os.MkdirAll(st.Dir, 0o700); err != nil {
		return nil, err
	}
	return st, nil
}

// ForBranch resolves branch paths without creating files or directories.
func ForBranch(root, branch string) *Store {
	return &Store{Root: root, Branch: branch, Dir: filepath.Join(repoStateRoot(root), branchSlug(branch))}
}

// Ensure creates the branch data directory.
func (s *Store) Ensure() error { return os.MkdirAll(s.Dir, 0o700) }

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

// WriteConclusion atomically replaces the final pull-request summary.
func (s *Store) WriteConclusion(body string) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	path := s.Conclusion()
	f, err := os.CreateTemp(s.Dir, ".conclusion-*.md")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := f.WriteString(strings.TrimSpace(body) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}

// GitHubCache is the path to mutable remote state kept separate from the timeline.
func (s *Store) GitHubCache() string { return filepath.Join(s.Dir, "github.json") }

// NavigatorCache is the repository-wide PR list and remote snapshot cache.
func NavigatorCache(root string) string { return filepath.Join(repoStateRoot(root), "github-prs.json") }

// PullRequestReviewDraft is an unpublished review isolated by pull-request number.
func PullRequestReviewDraft(root string, number int) string {
	return filepath.Join(repoStateRoot(root), "reviews", fmt.Sprintf("%d.json", number))
}

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
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(legacy, entry.Name()), target); err != nil {
			return err
		}
	}
	_ = os.Remove(legacy)
	return nil
}

// sharedRoots caches the per-checkout repository identity: resolving it runs
// one git subprocess, and repoStateRoot sits on hot paths.
var sharedRoots sync.Map // absolute checkout root -> shared identity path

// sharedRepoPath identifies the repository regardless of which worktree is
// open: the parent of the common .git directory. For the main checkout that
// is the checkout root itself (so existing state directories keep working);
// linked worktrees map to the main checkout instead of getting their own
// duplicated caches. Non-repository roots (tests) fall back to the path.
func sharedRepoPath(root string) string {
	clean, err := filepath.Abs(root)
	if err != nil {
		clean = root
	}
	if cached, ok := sharedRoots.Load(clean); ok {
		return cached.(string)
	}
	path := clean
	if common, err := git.CommonDir(clean); err == nil {
		path = filepath.Dir(common)
	}
	sharedRoots.Store(clean, path)
	return path
}

func repoStateRoot(root string) string {
	clean := sharedRepoPath(root)
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

// branchSlug disambiguates branch names that needed character replacement:
// feat/x and feat-x collapse to the same replacer output and must not share
// a state directory, so replaced names carry a short content hash. Names
// that survive slug() unchanged keep their plain directory.
func branchSlug(branch string) string {
	cleaned := slug(branch)
	if cleaned == branch {
		return cleaned
	}
	hash := sha256.Sum256([]byte(branch))
	return cleaned + "-" + hex.EncodeToString(hash[:])[:8]
}

// ReviewedMarksPath locates the reviewed-file marks for one review scope: the
// pull request when its number is known, else the local branch. Marks are
// per-scope files so moving between PRs (stacked ones included) never leaks
// another review's progress.
func ReviewedMarksPath(root string, prNumber int, branch string) string {
	name := "branch-" + branchSlug(branch)
	if prNumber > 0 {
		name = strconv.Itoa(prNumber)
	}
	return filepath.Join(repoStateRoot(root), "reviewed", name+".json")
}

// LoadReviewedMarks reads a marks file: a JSON object of path → fingerprint.
// A missing file is an empty set.
func LoadReviewedMarks(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	marks := map[string]string{}
	if err := json.Unmarshal(data, &marks); err != nil {
		return nil, fmt.Errorf("parse reviewed marks %s: %w", path, err)
	}
	return marks, nil
}

// SaveReviewedMarks writes the marks atomically so an external reviewer
// reading the same file never observes a partial write.
func SaveReviewedMarks(path string, marks map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(marks, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".reviewed-*.json")
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

// replaceFile renames tmp over path, with the Windows remove-and-retry that
// WriteConclusion also needs.
func replaceFile(tmp, path string) error {
	err := os.Rename(tmp, path)
	if err != nil && runtime.GOOS == "windows" {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(tmp, path)
	}
	return err
}
