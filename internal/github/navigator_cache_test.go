package github

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNavigatorCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".live-pr", "github-prs.json")
	cache := NewNavigatorCache()
	cache.Repository = "acme/repo"
	cache.ViewerLogin = "octocat"
	cache.FetchedStates["OPEN"] = true
	cache.FetchedStates["CLOSED"] = true
	cache.PRs = []PR{{Number: 12, HeadRefName: "feature/x", ReviewRequests: []PRUser{{Login: "octocat"}}}}
	cache.SetView("Assigned", cache.PRs, 42, "2026-08-08T00:00:00Z")
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
	viewPRs, view, viewOK := got.View("Assigned")
	if got.Repository != "acme/repo" || got.ViewerLogin != "octocat" || !got.FetchedStates["OPEN"] || !got.FetchedStates["CLOSED"] || len(got.PRs) != 1 || got.PRs[0].HeadRefName != "feature/x" || len(got.PRs[0].ReviewRequests) != 1 || !viewOK || view.TotalCount != 42 || len(viewPRs) != 1 || !ok || snapshot.PR.Number != 12 || snapshot.PR.Body != "" || len(snapshot.Comments) != 1 {
		t.Fatalf("navigator cache = %#v snapshot=%#v", got, snapshot)
	}
}

func TestNavigatorCacheLoadsLegacyAggregateWithoutViewState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-prs.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"prs_state":"OPEN","prs":[{"number":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cache, err := LoadNavigatorCache(path)
	if err != nil || cache.Views == nil || len(cache.PRs) != 1 {
		t.Fatalf("legacy cache = %#v err=%v", cache, err)
	}
}

func TestLoadNavigatorCacheResetsOnVersionMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "navigator.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"prs":[{"number":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cache, err := LoadNavigatorCache(path)
	if err != nil {
		t.Fatalf("version mismatch must reset, not fail: %v", err)
	}
	if len(cache.PRs) != 0 || cache.Version != CacheVersion || cache.PRsState != "OPEN" {
		t.Fatalf("expected fresh cache, got %#v", cache)
	}
}

func TestNavigatorCachePrunesRowsOutsideCachedViews(t *testing.T) {
	cache := NewNavigatorCache()
	cache.PRs = []PR{{Number: 1}, {Number: 2}, {Number: 3}}
	cache.SetView("Assigned", []PR{{Number: 2}}, 1, "now")
	cache.SetView("Closed", []PR{{Number: 3}}, 1, "now")
	cache.PrunePRs()
	if len(cache.PRs) != 2 || cache.PRs[0].Number != 2 || cache.PRs[1].Number != 3 {
		t.Fatalf("pruned PRs = %#v", cache.PRs)
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

func TestCloneIsolatesFromLaterMutations(t *testing.T) {
	c := NewNavigatorCache()
	c.PRs = []PR{{Number: 1, Title: "one"}}
	c.SetView("all", c.PRs, 1, "t1")
	clone := c.Clone()

	c.PRs[0].Title = "mutated"
	c.SetView("all", nil, 0, "t2")
	c.SetSnapshot(PRSnapshot{PR: PR{Number: 2}})

	if clone.PRs[0].Title != "one" {
		t.Fatalf("clone PR mutated: %q", clone.PRs[0].Title)
	}
	if view, ok := clone.Views["all"]; !ok || view.TotalCount != 1 {
		t.Fatalf("clone view mutated: %#v ok=%v", view, ok)
	}
	if len(clone.Snapshots) != 0 {
		t.Fatalf("clone snapshots mutated: %d", len(clone.Snapshots))
	}
}
