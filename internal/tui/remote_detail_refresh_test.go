package tui

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"

	"github.com/shonenm/live-pr/v2/internal/embeddedterm"
	gh "github.com/shonenm/live-pr/v2/internal/github"
)

// A remote refresh advances the generation without resetCaches, so a raw-diff
// ticket from the previous generation must not suppress the fresh dispatch.
func TestStaleRawTicketDoesNotBlockNextGeneration(t *testing.T) {
	m := testModel()
	m.remote, m.targetGeneration = true, 5
	load := func() (string, error) { return "diff", nil }
	if _, _, _, cmd := m.cachedRawDetail("k", load); cmd == nil {
		t.Fatal("first load was not dispatched")
	}
	if _, _, _, cmd := m.cachedRawDetail("k", load); cmd != nil {
		t.Fatal("same-generation load should be single-flight")
	}

	m.targetGeneration++
	if _, _, _, cmd := m.cachedRawDetail("k", load); cmd == nil {
		t.Fatal("stale ticket blocked the load for the new generation")
	}

	m, _ = m.handleRawDetailLoaded(rawDetailLoaded{generation: 5, key: "k", raw: "old"})
	if _, cached := m.detailView.rawCache["k"]; cached || !pendingFor(m.detailView.rawPending, "k", 6) {
		t.Fatalf("stale result touched the fresh load: cache=%v pending=%v", m.detailView.rawCache, m.detailView.rawPending)
	}
	m, _ = m.handleRawDetailLoaded(rawDetailLoaded{generation: 6, key: "k", raw: "new"})
	if m.detailView.rawCache["k"] != "new" || len(m.detailView.rawPending) != 0 {
		t.Fatalf("fresh result not applied: cache=%v pending=%v", m.detailView.rawCache, m.detailView.rawPending)
	}
}

func TestStaleRawResultReleasesItsOwnTicket(t *testing.T) {
	m := testModel()
	m.remote, m.targetGeneration = true, 6
	m.detailView.rawPending = map[string]uint64{"k": 5}
	m, _ = m.handleRawDetailLoaded(rawDetailLoaded{generation: 5, key: "k", raw: "old"})
	if len(m.detailView.rawPending) != 0 || len(m.detailView.rawCache) != 0 {
		t.Fatalf("stale result: cache=%v pending=%v", m.detailView.rawCache, m.detailView.rawPending)
	}
}

// isLoading counts diffPending, so a ticket nobody releases keeps the spinner
// running until the PR is reopened.
func TestStaleDiffRenderReleasesItsOwnTicket(t *testing.T) {
	m := testModel()
	m.remote, m.targetGeneration = true, 6
	m.detailView.diffPending = map[string]uint64{"d": 5}
	m, _ = m.handleDiffRendered(diffRendered{generation: 5, key: "d", output: "old"})
	if len(m.detailView.diffPending) != 0 || m.isLoading() {
		t.Fatalf("stale render left its ticket: pending=%v loading=%v", m.detailView.diffPending, m.isLoading())
	}
	if _, cached := m.detailView.diffCache["d"]; cached {
		t.Fatal("stale render was cached")
	}

	m.detailView.diffPending = map[string]uint64{"d": 6}
	m, _ = m.handleDiffRendered(diffRendered{generation: 5, key: "d", output: "old"})
	if !pendingFor(m.detailView.diffPending, "d", 6) {
		t.Fatal("stale render released the ticket of the fresh dispatch")
	}
}

func TestRemoteRefsReloadClosesTerminalAndKeepsCommitReview(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("embedded PTY is not supported on Windows")
	}
	m := testModel()
	// Only the commit command is set: a replacement built from diffCommand
	// would be nil.
	m.root, m.diffCommand, m.diffCommitCommand = t.TempDir(), "", "cat"
	m.remote, m.refreshing, m.targetGeneration = true, true, 5
	m.cache.PR = &gh.PR{Number: 12}
	m.detailView.reviewSHA = "abc1234"
	oldTerminal := embeddedterm.New("cat", m.root, nil)
	m.diffTerminal = oldTerminal
	t.Cleanup(oldTerminal.Close)
	defer m.close()

	u, _ := m.Update(remoteRefsLoaded{generation: 5, number: 12, headRef: "refs/live-pr/pulls/12/head", headOID: "deadbeef", base: "origin/main", diffBase: "origin/main"})
	m = u.(Model)
	if oldTerminal.Available() {
		t.Fatal("remote refresh left the previous review terminal open")
	}
	if m.diffTerminal == nil || m.diffTerminal == oldTerminal {
		t.Fatal("remote refresh did not rebuild the commit review terminal")
	}
	if m.detailView.reviewSHA != "abc1234" {
		t.Fatalf("reviewSHA = %q", m.detailView.reviewSHA)
	}
}

func TestOpenRemoteDoesNotResolveBaseSynchronously(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "base"},
		{"update-ref", "refs/remotes/origin/main", "HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// git.ResolveBase would answer origin/main here; openRemote must not ask.
	m := testModel()
	_ = m.openRemote(gh.PR{Number: 7, BaseRefName: "main", HeadRefName: "feature"})
	if m.detailView.base != "main" || m.detailView.diffBase != "main" || m.detailView.reviewRange != "main...refs/live-pr/pulls/7/head" {
		t.Fatalf("placeholders = base:%q diffBase:%q range:%q", m.detailView.base, m.detailView.diffBase, m.detailView.reviewRange)
	}

	// The resolved base still lands when the pull ref cannot be fetched.
	u, _ := m.Update(remoteRefsLoaded{generation: m.targetGeneration, number: 7, base: "origin/main", diffBase: "origin/main", err: errors.New("fetch failed")})
	m = u.(Model)
	if m.detailView.base != "origin/main" || m.detailView.diffBase != "origin/main" || m.detailView.reviewRange != "origin/main...refs/live-pr/pulls/7/head" {
		t.Fatalf("failed refs = base:%q diffBase:%q range:%q", m.detailView.base, m.detailView.diffBase, m.detailView.reviewRange)
	}

	_ = m.openRemote(gh.PR{Number: 8, BaseRefName: "main", BaseRefOID: "0123abc", HeadRefName: "feature"})
	if m.detailView.base != "main" || m.detailView.diffBase != "0123abc" {
		t.Fatalf("OID placeholders = base:%q diffBase:%q", m.detailView.base, m.detailView.diffBase)
	}
}
