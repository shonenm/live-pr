// Package git wraps the handful of git commands live-pr needs by shelling out,
// rather than pulling in a full git library.
package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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

// CommonDir returns the absolute path of the repository's common .git
// directory for the checkout at dir. Linked worktrees share it with their
// main checkout, so it identifies the repository across worktrees.
func CommonDir(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("git rev-parse --git-common-dir: %w: %s", err, detail)
		}
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), nil
}

// CurrentBranch returns the checked-out branch name (e.g. "main", "feature/x").
func CurrentBranch() (string, error) {
	return run("rev-parse", "--abbrev-ref", "HEAD")
}

// Revision resolves rev to its full object ID.
func Revision(rev string) (string, error) { return run("rev-parse", "--verify", rev+"^{commit}") }

// IsAncestor reports whether ancestor is reachable from descendant.
func IsAncestor(ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", "--end-of-options", ancestor, descendant)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
	}
	return true, nil
}

// Push pushes the branch to origin, setting upstream. A stalled SSH/HTTPS
// transport must not hang the caller forever, so the push is bounded; the
// limit is longer than FetchPull's because a push carries data up.
func Push(branch string) error {
	if done := debugtime.Start("git push"); done != nil {
		defer done()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "push", "-u", "origin", branch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if _, err := cmd.Output(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("git push -u origin %s: %w: %s", branch, err, detail)
		}
		return fmt.Errorf("git push -u origin %s: %w", branch, err)
	}
	return nil
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
	out, err := run("merge-base", "--end-of-options", base, head)
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
		"--end-of-options", base+".."+head)
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
	behindText, err := run("rev-list", "--count", "--end-of-options", head+".."+base)
	if err != nil {
		return MergeReadiness{}, err
	}
	var readiness MergeReadiness
	if _, err := fmt.Sscan(behindText, &readiness.Behind); err != nil {
		return MergeReadiness{}, fmt.Errorf("parse commits behind %q: %w", behindText, err)
	}
	cmd := exec.Command("git", "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", "--end-of-options", base, head)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, mergeErr := cmd.Output()
	if mergeErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(mergeErr, &exitErr) {
			detail := strings.TrimSpace(stderr.String())
			if detail != "" {
				return MergeReadiness{}, fmt.Errorf("git merge-tree %s %s: %w: %s", base, head, mergeErr, detail)
			}
			return MergeReadiness{}, fmt.Errorf("git merge-tree %s %s: %w", base, head, mergeErr)
		}
		readiness.ConflictFiles = parseMergeTreeConflicts(out)
		if len(readiness.ConflictFiles) == 0 {
			readiness.ConflictFiles = parseMergeTreeConflicts(stderr.Bytes())
		}
	}
	if len(readiness.ConflictFiles) > 0 {
		return readiness, nil
	}
	extra, err := contentConflicts(base, head)
	if err != nil {
		return readiness, err
	}
	readiness.ConflictFiles = extra
	return readiness, nil
}

func parseMergeTreeConflicts(out []byte) []string {
	var files []string
	seen := map[string]bool{}
	for i, part := range bytes.Split(out, []byte{0}) {
		path := strings.TrimSpace(string(part))
		if path == "" || seen[path] || i == 0 && looksLikeOID(path) {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	return files
}

const (
	maxContentConflictFiles = 32
	maxContentConflictBlob  = 1 << 20
)

func contentConflicts(base, head string) ([]string, error) {
	mergeBase, err := MergeBase(base, head)
	if err != nil {
		return nil, err
	}
	baseFiles, err := changedPaths(mergeBase, base)
	if err != nil {
		return nil, err
	}
	headFiles, err := changedPaths(mergeBase, head)
	if err != nil {
		return nil, err
	}
	overlap := make([]string, 0, min(len(baseFiles), len(headFiles)))
	for path := range baseFiles {
		if headFiles[path] {
			overlap = append(overlap, path)
		}
	}
	sort.Strings(overlap)
	if len(overlap) > maxContentConflictFiles {
		overlap = overlap[:maxContentConflictFiles]
	}
	dir, err := os.MkdirTemp("", "live-pr-merge-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var files []string
	for _, path := range overlap {
		ours, oursOK := blob(head, path)
		theirs, theirsOK := blob(base, path)
		ancestor, ancestorOK := blob(mergeBase, path)
		if !oursOK || !theirsOK {
			// The path changed on both sides but is absent on one: a
			// modify/delete overlap, which is a conflict. (Absent on both
			// is delete/delete, which is not.)
			if oursOK != theirsOK {
				files = append(files, path)
			}
			continue
		}
		if len(ours) > maxContentConflictBlob || len(theirs) > maxContentConflictBlob {
			continue
		}
		if !ancestorOK {
			ancestor = nil
		}
		oursPath := filepath.Join(dir, "ours")
		basePath := filepath.Join(dir, "base")
		theirsPath := filepath.Join(dir, "theirs")
		if err := os.WriteFile(oursPath, ours, 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(basePath, ancestor, 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(theirsPath, theirs, 0o600); err != nil {
			return nil, err
		}
		cmd := exec.Command("git", "merge-file", "--quiet", oursPath, basePath, theirsPath)
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
				files = append(files, path)
				continue
			}
		}
	}
	return files, nil
}

func changedPaths(from, to string) (map[string]bool, error) {
	out, err := run("diff", "--name-only", "-z", from, to)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, path := range strings.Split(out, "\x00") {
		if path != "" {
			paths[path] = true
		}
	}
	return paths, nil
}

func blob(rev, path string) ([]byte, bool) {
	out, err := exec.Command("git", "show", rev+":"+path).Output()
	if err != nil {
		return nil, false
	}
	return out, true
}

func looksLikeOID(value string) bool {
	if len(value) < 16 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// DiffStats returns file and line counts for the given range. An empty head
// compares the working tree against base. Binary files count toward Files but
// not line totals.
func DiffStats(base, head string) (ChangeStats, error) {
	rangeSpec := base
	if head != "" {
		rangeSpec = base + "..." + head
	}
	out, err := run("diff", "--numstat", "--end-of-options", rangeSpec)
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
	if head == "" {
		root, _ := RepoRoot()
		untracked, _ := run("ls-files", "--others", "--exclude-standard", "-z")
		for _, path := range strings.Split(untracked, "\x00") {
			if path == "" {
				continue
			}
			stats.Files++
			full := filepath.Join(root, path)
			if info, err := os.Lstat(full); err == nil && info.Mode()&os.ModeSymlink != 0 {
				stats.Additions++
				continue
			}
			data, err := os.ReadFile(full)
			if err != nil || bytes.IndexByte(data, 0) >= 0 {
				continue
			}
			if len(data) > 0 {
				stats.Additions += bytes.Count(data, []byte{'\n'})
				if data[len(data)-1] != '\n' {
					stats.Additions++
				}
			}
		}
	}
	return stats, nil
}

// LocalState identifies the checked-out branch and every local Git input that
// can change the review (HEAD, index, worktree, and untracked paths).
type LocalState struct {
	Branch      string
	Fingerprint string
}

func CurrentLocalState() (LocalState, error) {
	branch, err := CurrentBranch()
	if err != nil {
		return LocalState{}, err
	}
	status, err := run("status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal")
	if err != nil {
		return LocalState{}, err
	}
	sum := sha256.Sum256([]byte(status))
	return LocalState{Branch: branch, Fingerprint: hex.EncodeToString(sum[:])}, nil
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
	cmd := exec.Command("git", "diff", "--quiet", "--end-of-options", base+"..."+head, "--")
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
	// Fingerprint identifies this file's diff content. It changes only when
	// this file's own diff changes, so reviewed marks survive commits that
	// touch other files.
	Fingerprint string
}

// ChangedFilesRange returns changed paths in the given range. An empty head
// compares the working tree against base and includes untracked files.
func ChangedFilesRange(base, head string) ([]ChangedFile, error) {
	rangeSpec := base
	if head != "" {
		rangeSpec = base + "..." + head
	}
	// --raw carries the pre/post blob IDs alongside the status, which gives
	// each file a diff fingerprint at no extra cost.
	out, err := run("diff", "--raw", "-z", "--end-of-options", rangeSpec)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var files []ChangedFile
	var root string
	for i := 0; i < len(parts) && parts[i] != ""; {
		meta := strings.Fields(strings.TrimPrefix(parts[i], ":"))
		i++
		// :<srcmode> <dstmode> <srcblob> <dstblob> <status>
		if len(meta) < 5 || i >= len(parts) {
			break
		}
		srcBlob, dstBlob, status := meta[2], meta[3], meta[4]
		file := ChangedFile{Status: status, Path: parts[i]}
		i++
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i >= len(parts) {
				break
			}
			file.OldPath, file.Path = file.Path, parts[i]
			i++
		}
		if root == "" {
			// Paths in --raw output are repo-root relative; resolve the root
			// once per scan so hashing works from subdirectory launches too.
			root, _ = RepoRoot()
		}
		file.Fingerprint = fileFingerprint(srcBlob, dstBlob, root, file.Path)
		files = append(files, file)
	}
	if head == "" {
		untracked, err := run("ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		if root == "" {
			root, _ = RepoRoot()
		}
		for _, path := range strings.Split(untracked, "\x00") {
			if path == "" {
				continue
			}
			files = append(files, ChangedFile{Status: "A", Path: path, Fingerprint: "untracked:" + workingTreeHash(filepath.Join(root, path))})
		}
	}
	return files, nil
}

// fileFingerprint identifies a file's diff content. Blob IDs alone cover
// committed revisions; an uncommitted post-image has no blob, so the working
// tree's content is hashed instead. An unreadable file (a deletion) keeps the
// null ID, which is still stable for that state.
func fileFingerprint(srcBlob, dstBlob, root, path string) string {
	if strings.Trim(dstBlob, "0") == "" {
		if root != "" {
			path = filepath.Join(root, path)
		}
		if sum := workingTreeHash(path); sum != "" {
			dstBlob = sum
		}
	}
	return srcBlob + ":" + dstBlob
}

type hashMemoEntry struct {
	modTime time.Time
	size    int64
	sum     string
}

// hashMemo caches working-tree hashes by absolute path; scans re-run on every
// refresh and re-reading each uncommitted file in full every time adds up.
var (
	hashMemoMu sync.Mutex
	hashMemo   = map[string]hashMemoEntry{}
)

// workingTreeHash hashes a working-tree file, memoized on (mtime, size).
func workingTreeHash(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return ""
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256([]byte(target))
		return hex.EncodeToString(sum[:])
	}
	hashMemoMu.Lock()
	entry, ok := hashMemo[path]
	hashMemoMu.Unlock()
	if ok && entry.modTime.Equal(info.ModTime()) && entry.size == info.Size() {
		return entry.sum
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	entry = hashMemoEntry{modTime: info.ModTime(), size: info.Size(), sum: hex.EncodeToString(sum[:])}
	hashMemoMu.Lock()
	hashMemo[path] = entry
	hashMemoMu.Unlock()
	return entry.sum
}

// FileDiffRange returns the colorized base...head patch for selected paths.
// An empty head compares through the working tree and includes selected
// untracked files without changing the index.
func FileDiffRange(base, head string, paths ...string) (string, error) {
	rangeSpec := base
	if head != "" {
		rangeSpec = base + "..." + head
	}
	args := []string{"diff", "--color=always", "--end-of-options", rangeSpec, "--"}
	args = append(args, paths...)
	out, err := run(args...)
	if err != nil {
		return "", err
	}
	if head == "" {
		untracked, _ := run("ls-files", "--others", "--exclude-standard", "-z")
		untrackedSet := map[string]bool{}
		for _, path := range strings.Split(untracked, "\x00") {
			untrackedSet[path] = path != ""
		}
		selected := paths
		if len(selected) == 0 {
			selected = make([]string, 0, len(untrackedSet))
			for path := range untrackedSet {
				selected = append(selected, path)
			}
			sort.Strings(selected)
		}
		for _, path := range selected {
			if !untrackedSet[path] {
				continue
			}
			cmd := exec.Command("git", "diff", "--no-index", "--color=always", "--", os.DevNull, path)
			data, diffErr := cmd.Output()
			var exitErr *exec.ExitError
			if diffErr != nil && !(errors.As(diffErr, &exitErr) && exitErr.ExitCode() == 1) {
				continue
			}
			if len(data) > 0 {
				if out != "" {
					out += "\n"
				}
				out += strings.TrimSpace(string(data))
			}
		}
	}
	return truncate(out, 800), nil
}

// Show returns the full `git show` for a commit (stat + colorized patch),
// capped to a sane number of lines.
func Show(sha string) (string, error) {
	out, err := run("show", "--color=always", "--stat", "-p", "--end-of-options", sha)
	if err != nil {
		return "", err
	}
	return truncate(out, 800), nil
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
func FetchPull(number int, base string) (string, string, error) {
	if done := debugtime.Start("git fetch pull"); done != nil {
		defer done()
	}
	if number <= 0 {
		return "", "", fmt.Errorf("invalid pull request number %d", number)
	}
	if _, err := run("check-ref-format", "--branch", base); err != nil {
		return "", "", fmt.Errorf("invalid pull request base %q", base)
	}
	headRef := fmt.Sprintf("refs/live-pr/pulls/%d/head", number)
	baseSpec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", base, base)
	headSpec := fmt.Sprintf("+refs/pull/%d/head:%s", number, headRef)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "--no-tags", "origin", baseSpec, headSpec)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("git fetch PR #%d: %w: %s", number, err, strings.TrimSpace(string(out)))
	}
	oid, err := run("rev-parse", headRef)
	if err != nil {
		return "", "", fmt.Errorf("resolve fetched PR #%d: %w", number, err)
	}
	return headRef, oid, nil
}

// CheckoutPullHead checks out the head branch of a same-repository pull
// request with plain git, mirroring gh pr checkout's non-fork path: fetch the
// branch from origin, then switch to a local tracking branch, fast-forwarding
// one that already exists (a diverged branch fails the same way it does under
// gh). Fork heads need gh's cross-repository remote wiring and must not come
// here.
func CheckoutPullHead(branch string) error {
	if done := debugtime.Start("git checkout pull head"); done != nil {
		defer done()
	}
	if _, err := run("check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid pull request head %q", branch)
	}
	refSpec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "--no-tags", "origin", refSpec)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
	}
	if _, err := run("rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		if _, err := run("switch", "--end-of-options", branch); err != nil {
			return err
		}
		_, err := run("merge", "--ff-only", "--end-of-options", "refs/remotes/origin/"+branch)
		return err
	}
	_, err := run("switch", "-c", branch, "--track", "--end-of-options", "origin/"+branch)
	return err
}
