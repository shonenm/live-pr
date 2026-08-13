// Package git wraps the handful of git commands live-pr needs by shelling out,
// rather than pulling in a full git library.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/shonenm/live-pr/internal/debugtime"
)

func run(args ...string) (string, error) {
	if done := debugtime.Start("git " + args[0]); done != nil {
		defer done()
	}
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
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

// ResolveBase turns a GitHub base name into the freshest local Git revision,
// preferring its origin remote-tracking ref when available.
func ResolveBase(base string) string {
	if base == "" {
		return DefaultBase()
	}
	if strings.HasPrefix(base, "origin/") {
		return base
	}
	if _, err := run("rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+base); err == nil {
		return "origin/" + base
	}
	return base
}

// MergeBase returns the best common ancestor of two revisions.
func MergeBase(base, head string) (string, error) {
	out, err := run("merge-base", base, head)
	if err != nil {
		return "", fmt.Errorf("git merge-base %s %s: %w", base, head, err)
	}
	return out, nil
}

// Commit is a git commit reduced to what the timeline needs.
type Commit struct {
	SHA     string
	Date    string // "2006-01-02T15:04"
	Subject string
	Body    string
}

// Commits returns the commits in base..HEAD, oldest first.
func Commits(base string) ([]Commit, error) { return CommitsRange(base, "HEAD") }

// CommitsRange returns commits in base..head, oldest first.
func CommitsRange(base, head string) ([]Commit, error) {
	// \x1f separates fields, \x1e separates records (so bodies may contain \n).
	out, err := run("log", "--reverse",
		"--date=format:%Y-%m-%dT%H:%M",
		"--format=%h%x1f%ad%x1f%s%x1f%b%x1e",
		base+".."+head)
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

// ChangeStats summarizes a diff range for PR list metadata.
type ChangeStats struct {
	Files, Additions, Deletions int
}

// MergeReadiness describes how far head trails base and which paths conflict.
type MergeReadiness struct {
	Behind        int
	ConflictFiles []string
}

// CheckMergeReadiness simulates merging head into base without touching HEAD,
// the index, or the worktree.
func CheckMergeReadiness(base, head string) (MergeReadiness, error) {
	behindText, err := run("rev-list", "--count", head+".."+base)
	if err != nil {
		return MergeReadiness{}, err
	}
	var readiness MergeReadiness
	if _, err := fmt.Sscan(behindText, &readiness.Behind); err != nil {
		return MergeReadiness{}, fmt.Errorf("parse commits behind %q: %w", behindText, err)
	}
	cmd := exec.Command("git", "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", base, head)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, mergeErr := cmd.Output()
	if mergeErr == nil {
		return readiness, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(mergeErr, &exitErr) || exitErr.ExitCode() != 1 {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return MergeReadiness{}, fmt.Errorf("git merge-tree %s %s: %w: %s", base, head, mergeErr, detail)
		}
		return MergeReadiness{}, fmt.Errorf("git merge-tree %s %s: %w", base, head, mergeErr)
	}
	parts := bytes.Split(out, []byte{0})
	for _, path := range parts[1:] { // the first field is the synthetic merge tree
		if len(path) > 0 {
			readiness.ConflictFiles = append(readiness.ConflictFiles, string(path))
		}
	}
	return readiness, nil
}

// DiffStats returns file and line counts for the given range. An empty head
// compares the working tree against base. Binary files count toward Files but
// not line totals.
func DiffStats(base, head string) (ChangeStats, error) {
	rangeSpec := base
	if head != "" {
		rangeSpec = base + "..." + head
	}
	out, err := run("diff", "--numstat", rangeSpec)
	if err != nil {
		return ChangeStats{}, err
	}
	var stats ChangeStats
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		var added, deleted string
		if _, err := fmt.Sscanf(line, "%s\t%s", &added, &deleted); err != nil {
			continue
		}
		stats.Files++
		if added != "-" {
			var n int
			_, _ = fmt.Sscanf(added, "%d", &n)
			stats.Additions += n
		}
		if deleted != "-" {
			var n int
			_, _ = fmt.Sscanf(deleted, "%d", &n)
			stats.Deletions += n
		}
	}
	return stats, nil
}

// HasUncommittedChanges reports whether the index, worktree, or untracked files differ from HEAD.
func HasUncommittedChanges() (bool, error) {
	out, err := run("status", "--porcelain", "--untracked-files=normal")
	return out != "", err
}

// HasChanges reports whether base...head contains any changed paths.
func HasChanges(base, head string) (bool, error) {
	if done := debugtime.Start("git diff --quiet"); done != nil {
		defer done()
	}
	cmd := exec.Command("git", "diff", "--quiet", base+"..."+head, "--")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return true, nil
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return false, fmt.Errorf("git diff --quiet: %w: %s", err, detail)
		}
		return false, fmt.Errorf("git diff --quiet: %w", err)
	}
	return false, nil
}

// ChangedFile is one entry in base...HEAD.
type ChangedFile struct {
	Status  string
	Path    string
	OldPath string
}

// ChangedFiles returns changed paths in base...HEAD order.
func ChangedFiles(base string) ([]ChangedFile, error) { return ChangedFilesRange(base, "HEAD") }

// ChangedFilesRange returns changed paths in the given range. An empty head
// compares the working tree against base.
func ChangedFilesRange(base, head string) ([]ChangedFile, error) {
	rangeSpec := base
	if head != "" {
		rangeSpec = base + "..." + head
	}
	out, err := run("diff", "--name-status", "-z", rangeSpec)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var files []ChangedFile
	for i := 0; i < len(parts) && parts[i] != ""; {
		status := parts[i]
		i++
		if i >= len(parts) {
			break
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i+1 >= len(parts) {
				break
			}
			files = append(files, ChangedFile{Status: status, OldPath: parts[i], Path: parts[i+1]})
			i += 2
			continue
		}
		files = append(files, ChangedFile{Status: status, Path: parts[i]})
		i++
	}
	return files, nil
}

// FileDiff returns the colorized base...HEAD patch for the selected paths.
func FileDiff(base string, paths ...string) string { return FileDiffRange(base, "HEAD", paths...) }

// FileDiffRange returns the colorized base...head patch for selected paths.
func FileDiffRange(base, head string, paths ...string) string {
	args := []string{"diff", "--color=always", base + "..." + head, "--"}
	args = append(args, paths...)
	out, err := run(args...)
	if err != nil {
		return ""
	}
	return truncate(out, 800)
}

// Show returns the full `git show` for a commit (stat + colorized patch),
// capped to a sane number of lines. Empty string if the sha is unresolvable.
func Show(sha string) string {
	out, err := run("show", "--color=always", "--stat", "-p", sha)
	if err != nil {
		return ""
	}
	return truncate(out, 800)
}

func truncate(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "… (truncated)")
	}
	return strings.Join(lines, "\n")
}

// FetchPull fetches a GitHub pull ref and its base without changing HEAD,
// the index, or the worktree. It returns the namespaced local head ref.
func FetchPull(number int, base, expectedOID string) (string, error) {
	if done := debugtime.Start("git fetch pull"); done != nil {
		defer done()
	}
	if number <= 0 {
		return "", fmt.Errorf("invalid pull request number %d", number)
	}
	if _, err := run("check-ref-format", "--branch", base); err != nil {
		return "", fmt.Errorf("invalid pull request base %q", base)
	}
	headRef := fmt.Sprintf("refs/live-pr/pulls/%d/head", number)
	baseSpec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", base, base)
	headSpec := fmt.Sprintf("+refs/pull/%d/head:%s", number, headRef)
	cmd := exec.Command("git", "fetch", "--no-tags", "origin", baseSpec, headSpec)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git fetch PR #%d: %w: %s", number, err, strings.TrimSpace(string(out)))
	}
	oid, err := run("rev-parse", headRef)
	if err != nil {
		return "", fmt.Errorf("resolve fetched PR #%d: %w", number, err)
	}
	if expectedOID != "" && oid != expectedOID {
		return "", fmt.Errorf("PR #%d moved during fetch (expected %s, got %s)", number, expectedOID, oid)
	}
	return headRef, nil
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
