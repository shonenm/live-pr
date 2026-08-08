package github

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheBase(t *testing.T) {
	cache := NewCache("feature")
	if got := cache.Base("main"); got != "main" {
		t.Fatalf("fallback base = %q", got)
	}
	cache.PR = &PR{BaseRefName: "release"}
	if got := cache.Base("main"); got != "release" {
		t.Fatalf("PR base = %q", got)
	}
}

func TestCacheRoundTripAndHeadIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "github.json")
	c := NewCache("feature/x")
	c.PR = &PR{Number: 12, URL: "https://example/pr/12", Author: PRUser{Login: "octocat"}, CreatedAt: "2026-08-07T14:49:25Z", Assignees: []PRUser{{Login: "alice"}}, Labels: []PRLabel{{Name: "bug", Color: "d73a4a"}}}
	c.Comments = []Comment{{ID: 42, Body: "cached comment"}}
	c.Activities = []Activity{{ID: 7, Event: "labeled"}}
	c.LastPublishedManagedBodyHash = "hash"
	if err := SaveCache(path, c); err != nil {
		t.Fatal(err)
	}

	got, err := LoadCache(path, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR == nil || got.PR.Number != 12 || got.PR.Author.Login != "octocat" || got.PR.CreatedAt != "2026-08-07T14:49:25Z" || len(got.PR.Assignees) != 1 || len(got.PR.Labels) != 1 || len(got.Comments) != 1 || got.Comments[0].ID != 42 || len(got.Activities) != 1 || got.Activities[0].Event != "labeled" || got.LastPublishedManagedBodyHash != "hash" {
		t.Fatalf("unexpected cache: %#v", got)
	}
	other, err := LoadCache(path, "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if other.PR != nil || other.Head != "feature-x" {
		t.Fatalf("cache leaked across heads: %#v", other)
	}
}

func TestLoadLegacyVersionOneCacheWithoutOpeningMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"head":"feature","pr":{"number":1,"url":"https://example/pr/1","body":"legacy"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(path, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR == nil || got.PR.Body != "legacy" || got.PR.Author.Login != "" || got.PR.CreatedAt != "" {
		t.Fatalf("legacy cache = %#v", got)
	}
}

func TestSaveCacheReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github.json")
	first := NewCache("feature")
	first.PR = &PR{Number: 1}
	if err := SaveCache(path, first); err != nil {
		t.Fatal(err)
	}
	second := NewCache("feature")
	second.PR = &PR{Number: 2}
	if err := SaveCache(path, second); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(path, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR == nil || got.PR.Number != 2 {
		t.Fatalf("existing cache was not replaced: %#v", got)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".github-*.json"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files remain: %v, %v", matches, err)
	}
}

func TestLoadCacheRejectsMalformedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCache(path, "feature"); err == nil {
		t.Fatal("expected malformed cache error")
	}
}
