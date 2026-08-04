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

// Push pushes the branch to origin, setting upstream.
func Push(branch string) error {
	_, err := run("push", "-u", "origin", branch)
	return err
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

// Commit is a git commit reduced to what the timeline needs.
type Commit struct {
	SHA     string
	Date    string // "2006-01-02T15:04"
	Subject string
	Body    string
}

// Commits returns the commits in base..HEAD, oldest first. An empty range yields
// no commits; an unresolvable base returns an error.
func Commits(base string) ([]Commit, error) {
	// \x1f separates fields, \x1e separates records (so bodies may contain \n).
	out, err := run("log", "--reverse",
		"--date=format:%Y-%m-%dT%H:%M",
		"--format=%h%x1f%ad%x1f%s%x1f%b%x1e",
		base+"..HEAD")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 4)
		if len(f) < 3 {
			continue
		}
		c := Commit{SHA: f[0], Date: f[1], Subject: f[2]}
		if len(f) == 4 {
			c.Body = strings.TrimSpace(f[3])
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// Show returns the full `git show` for a commit (stat + colorized patch),
// capped to a sane number of lines. Empty string if the sha is unresolvable.
func Show(sha string) string {
	out, err := run("show", "--color=always", "--stat", "-p", sha)
	if err != nil {
		return ""
	}
	lines := strings.Split(out, "\n")
	if len(lines) > 800 {
		lines = append(lines[:800], "… (truncated)")
	}
	return strings.Join(lines, "\n")
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
