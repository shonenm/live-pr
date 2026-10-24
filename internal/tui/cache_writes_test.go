package tui

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
)

func TestCacheSnapshotsCannotRollBack(t *testing.T) {
	m := testModel()
	path := filepath.Join(t.TempDir(), "github.json")
	old := gh.NewCache("feature")
	old.PR = &gh.PR{Number: 1, HeadRefOID: "old"}
	stale := m.saveCacheCmd(path, old)
	current := old.Clone()
	current.PR.HeadRefOID = "new"
	current.PR.Checks = []gh.PRCheck{{Conclusion: "SUCCESS"}}
	current.Comments = []gh.Comment{{Body: "new comment"}}
	current.Reviews = []gh.Review{{Body: "new review"}}
	fresh := m.saveCacheCmd(path, current)
	if msg := fresh().(cacheSaved); msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg := stale().(cacheSaved); msg.err != nil {
		t.Fatal(msg.err)
	}
	got, err := gh.LoadCache(path, "feature")
	if err != nil || got.PR.HeadRefOID != "new" || len(got.Comments) != 1 || len(got.Reviews) != 1 || got.PR.Checks[0].Conclusion != "SUCCESS" {
		t.Fatalf("cache rolled back: %+v, %v", got, err)
	}
	other := filepath.Join(t.TempDir(), "github.json")
	if msg := m.saveCacheCmd(other, old)().(cacheSaved); msg.err != nil {
		t.Fatal(msg.err)
	}
}

func TestNavigatorSnapshotsCannotRollBack(t *testing.T) {
	m := testModel()
	path := filepath.Join(t.TempDir(), "navigator.json")
	cache := gh.NewNavigatorCache()
	old := m.saveNavigatorCacheCmd(path, cache)
	cache.PRs = []gh.PR{{Number: 42}}
	fresh := m.saveNavigatorCacheCmd(path, cache)
	if msg := fresh(); msg != nil {
		t.Fatal(msg)
	}
	if msg := old(); msg != nil {
		t.Fatal(msg)
	}
	got, err := gh.LoadNavigatorCache(path)
	if err != nil || len(got.PRs) != 1 {
		t.Fatalf("navigator rolled back: %+v %v", got, err)
	}
}

func TestCacheWritesConcurrentAndFailedSave(t *testing.T) {
	var w cacheWrites
	value := 0
	saves := make([]func() error, 100)
	for i := range saves {
		saves[i] = w.prepare("cache", func() error { value = i; return nil })
	}
	var wg sync.WaitGroup
	for _, save := range saves {
		wg.Go(func() {
			if err := save(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if value != 99 {
		t.Fatalf("latest value = %d", value)
	}
	older := w.prepare("cache", func() error { value = 100; return nil })
	failure := errors.New("disk full")
	failed := w.prepare("cache", func() error { return failure })
	if err := failed(); !errors.Is(err, failure) {
		t.Fatalf("error lost: %v", err)
	}
	if err := older(); err != nil {
		t.Fatal(err)
	}
	if value != 100 {
		t.Fatal("failed save suppressed a valid pending snapshot")
	}
}
