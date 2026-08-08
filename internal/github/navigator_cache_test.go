package github

import (
	"path/filepath"
	"testing"
)

func TestNavigatorCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".live-pr", "github-prs.json")
	cache := NewNavigatorCache()
	cache.ViewerLogin = "octocat"
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
	if got.ViewerLogin != "octocat" || len(got.PRs) != 1 || got.PRs[0].HeadRefName != "feature/x" || len(got.PRs[0].ReviewRequests) != 1 || !ok || snapshot.PR.Body != "description" || len(snapshot.Comments) != 1 {
		t.Fatalf("navigator cache = %#v snapshot=%#v", got, snapshot)
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
