package github

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestNavigatorCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".live-pr", "github-prs.json")
	cache := NewNavigatorCache()
	cache.ViewerLogin = "octocat"
	cache.FetchedStates["OPEN"] = true
	cache.FetchedStates["CLOSED"] = true
	cache.PRs = []PR{{Number: 12, HeadRefName: "feature/x", ReviewRequests: []PRUser{{Login: "octocat"}}}}
	cache.FetchedAt = "2026-08-08T00:00:00Z"
	cache.SetSnapshot(PRSnapshot{
		PR:       PR{Number: 12, Body: "description"},
		Comments: []Comment{{ID: 42, Body: "comment"}},
	})
	if err := SaveNavigatorCache(path, cache); err != nil {
		t.Fatal(err)
	}
	got, err := LoadNavigatorCache(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := got.Snapshot(12)
	if got.ViewerLogin != "octocat" || !got.FetchedStates["OPEN"] || !got.FetchedStates["CLOSED"] || len(got.PRs) != 1 || got.PRs[0].HeadRefName != "feature/x" || len(got.PRs[0].ReviewRequests) != 1 || !ok || snapshot.PR.Number != 12 || snapshot.PR.Body != "" || len(snapshot.Comments) != 1 {
		t.Fatalf("navigator cache = %#v snapshot=%#v", got, snapshot)
	}
}

func TestNavigatorCacheRetainsRecentSnapshots(t *testing.T) {
	cache := NewNavigatorCache()
	for i := 1; i <= maxNavigatorSnapshots+1; i++ {
		cache.SetSnapshot(PRSnapshot{PR: PR{Number: i}, FetchedAt: fmt.Sprintf("%04d", i)})
	}
	if len(cache.Snapshots) != maxNavigatorSnapshots {
		t.Fatalf("snapshot count = %d", len(cache.Snapshots))
	}
	if _, ok := cache.Snapshot(1); ok {
		t.Fatal("oldest snapshot was retained")
	}
	if _, ok := cache.Snapshot(maxNavigatorSnapshots + 1); !ok {
		t.Fatal("newest snapshot was evicted")
	}
}

func TestNavigatorCacheDoesNotContainPublishBaseline(t *testing.T) {
	cache := NewNavigatorCache()
	cache.SetSnapshot(PRSnapshot{PR: PR{Number: 1}})
	path := filepath.Join(t.TempDir(), "github-prs.json")
	if err := SaveNavigatorCache(path, cache); err != nil {
		t.Fatal(err)
	}
	got, err := LoadNavigatorCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Snapshot(1); !ok {
		t.Fatal("snapshot missing")
	}
}
