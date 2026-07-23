// Package git wraps the handful of git commands live-pr needs by shelling out,
// rather than pulling in a full git library.
package git

import (
	"os/exec"
	"strings"
)

func run(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// RepoRoot returns the absolute path to the current repository's top level.
func RepoRoot() (string, error) {
	return run("rev-parse", "--show-toplevel")
}

// CurrentBranch returns the checked-out branch name (e.g. "main", "feature/x").
func CurrentBranch() (string, error) {
	return run("rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBase returns the branch to compare against: the remote's default
// (origin/HEAD, e.g. "origin/main") when known, else a local main/master,
// falling back to "main".
func DefaultBase() string {
	if out, err := run("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		return out
	}
	for _, b := range []string{"main", "master"} {
		if _, err := run("rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return "main"
}

// ShowStat returns `git show --stat` for a commit, colorized, with a compact
// author/date header. Empty string if the sha cannot be resolved.
func ShowStat(sha string) string {
	out, err := run("show", "--stat", "--color=always",
		"--format=%C(dim)%an committed · %ad%C(reset)", "--date=short", sha)
	if err != nil {
		return ""
	}
	return out
}
