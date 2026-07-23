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
