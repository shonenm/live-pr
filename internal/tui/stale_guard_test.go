package tui

import (
	"errors"
	"testing"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
)

// Switching branches A→B→A leaves a rebuild for B in flight. Its result must
// not replace the model that is showing A again.
func TestStaleLocalBranchReloadCannotReplaceModel(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.localGeneration = 2
	m.checkoutReloading = true
	next := testModel()
	next.screen = prListScreen
	u, cmd := m.Update(localBranchReloaded{generation: 1, next: &next})
	got := u.(Model)
	if got.screen != detailScreen || got.notice != "" || got.localGeneration != 2 {
		t.Fatalf("stale branch reload replaced the model: screen=%v notice=%q localGeneration=%d", got.screen, got.notice, got.localGeneration)
	}
	if !got.checkoutReloading {
		t.Fatal("stale branch reload ended the reload that is still in flight")
	}
	if cmd != nil {
		t.Fatal("stale branch reload scheduled follow-up work")
	}

	u, cmd = m.Update(localBranchReloaded{generation: 1, err: errors.New("boom")})
	got = u.(Model)
	if got.status != "" || !got.checkoutReloading || cmd != nil {
		t.Fatalf("stale branch reload error was applied: status=%q checkoutReloading=%v", got.status, got.checkoutReloading)
	}
}

// A poll result from before a checkout must neither start a branch rebuild
// nor fork a second poll loop beside the current one.
func TestStaleLocalPollCannotStartBranchReload(t *testing.T) {
	m := testModel()
	m.currentBranch = "feature/a"
	m.localGeneration = 2
	m.targetGeneration = 5
	u, cmd := m.Update(localStatePolled{generation: 1, state: git.LocalState{Branch: "feature/b"}})
	got := u.(Model)
	if got.checkoutReloading || got.refreshing || got.targetGeneration != 5 {
		t.Fatalf("stale poll started a branch reload: checkoutReloading=%v refreshing=%v targetGeneration=%d", got.checkoutReloading, got.refreshing, got.targetGeneration)
	}
	if cmd != nil {
		t.Fatal("stale poll scheduled follow-up work")
	}

	u, cmd = m.Update(localStatePolled{generation: 1, err: errors.New("boom")})
	got = u.(Model)
	if got.status != "" || got.localPollError != "" || cmd != nil {
		t.Fatalf("stale poll error was applied: status=%q localPollError=%q", got.status, got.localPollError)
	}
}

// Reopening the same PR bumps the target generation; the conversation fetched
// for the earlier visit carries the same PR number and is told apart only by
// its generation.
func TestStaleConversationRefreshCannotReplaceComments(t *testing.T) {
	m := testModel()
	m.targetGeneration = 2
	m.refreshing = true
	m.cache.PR = &gh.PR{Number: 12, URL: "u"}
	m.cache.Comments = []gh.Comment{{ID: 1, Body: "current"}}
	u, cmd := m.Update(githubConversationRefreshed{generation: 1, number: 12, comments: []gh.Comment{{ID: 2, Body: "stale"}}})
	got := u.(Model)
	if len(got.cache.Comments) != 1 || got.cache.Comments[0].ID != 1 {
		t.Fatalf("stale conversation replaced the comments: %#v", got.cache.Comments)
	}
	if !got.refreshing || got.githubStatus != "" || got.cache.FetchedAt != "" {
		t.Fatalf("stale conversation touched refresh state: refreshing=%v githubStatus=%q fetchedAt=%q", got.refreshing, got.githubStatus, got.cache.FetchedAt)
	}
	if cmd != nil {
		t.Fatal("stale conversation scheduled follow-up work")
	}

	u, cmd = m.Update(githubConversationRefreshed{generation: 1, number: 12, err: errors.New("offline")})
	got = u.(Model)
	if !got.refreshing || got.githubStatus != "" || cmd != nil {
		t.Fatalf("stale conversation error was applied: refreshing=%v githubStatus=%q", got.refreshing, got.githubStatus)
	}
}

// A flush dispatched before the target changed still clears the in-flight
// flag, but its outcome is not reported against the current target.
func TestStaleOutboxFlushCannotReportOnCurrentTarget(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.targetGeneration = 2
	m.outboxFlushing = true
	m.cache.PR = &gh.PR{Number: 12, URL: "u"}
	u, cmd := m.Update(outboxFlushed{generation: 1, number: 12, err: errors.New("offline")})
	got := u.(Model)
	if got.outboxFlushing {
		t.Fatal("stale flush left the in-flight flag set")
	}
	if got.githubStatus != "" || got.notice != "" {
		t.Fatalf("stale flush reported on the current target: githubStatus=%q notice=%q", got.githubStatus, got.notice)
	}
	if cmd != nil {
		t.Fatal("stale flush scheduled follow-up work")
	}
}

// The pending ticket is keyed by diff key alone, so a raw diff requested for
// the previous target under the same key is rejected by generation.
func TestStaleRawDetailCannotFillCache(t *testing.T) {
	m := testModel()
	m.targetGeneration = 2
	m.detailView.rawPending = map[string]bool{"file:a.go": true}
	u, cmd := m.Update(rawDetailLoaded{generation: 1, key: "file:a.go", raw: "stale diff"})
	got := u.(Model)
	if _, cached := got.detailView.rawCache["file:a.go"]; cached {
		t.Fatalf("stale raw diff was cached: %q", got.detailView.rawCache["file:a.go"])
	}
	if !got.detailView.rawPending["file:a.go"] {
		t.Fatal("stale raw diff consumed the current request's ticket")
	}
	if cmd != nil {
		t.Fatal("stale raw diff scheduled follow-up work")
	}
}

func TestStaleListAvatarColorsAreDropped(t *testing.T) {
	m := testModel()
	m.prList.generation = 2
	m.avatarColors = map[string]string{"me": "#111111"}
	m.prList.rowCache = map[prRowCacheKey][]string{{}: {"row"}}
	u, cmd := m.Update(listAvatarColorsLoaded{generation: 1, colors: map[string]string{"me": "#222222", "you": "#333333"}})
	got := u.(Model)
	if len(got.avatarColors) != 1 || got.avatarColors["me"] != "#111111" {
		t.Fatalf("stale avatar colors were applied: %#v", got.avatarColors)
	}
	if len(got.prList.rowCache) != 1 {
		t.Fatal("stale avatar colors cleared the row cache")
	}
	if cmd != nil {
		t.Fatal("stale avatar colors scheduled follow-up work")
	}
}
