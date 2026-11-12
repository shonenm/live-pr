package tui

import (
	"errors"
	"path/filepath"
	"testing"

	gh "github.com/shonenm/live-pr/v2/internal/github"
	"github.com/shonenm/live-pr/v2/internal/publish"
)

// publishDoneModel is a detail screen mid-publish whose on-disk cache already
// holds what publish.Run wrote.
func publishDoneModel(t *testing.T) (Model, publishDone) {
	t.Helper()
	m := testModel()
	m.currentBranch = m.detailView.head
	m.cachePath = filepath.Join(t.TempDir(), "github.json")
	m.cache = gh.NewCache(m.detailView.head)
	m.cache.PR = &gh.PR{Number: 7, URL: "https://example/pr/7", Body: "old"}
	m.cache.LastPublishedManagedBodyHash = "old-hash"
	m.targetGeneration = 7
	m.publishing = true

	published := m.cache.Clone()
	published.PR = &gh.PR{Number: 7, URL: "https://example/pr/7", Body: "new"}
	published.LastPublishedManagedBodyHash = "new-hash"
	if err := gh.SaveCache(m.cachePath, published); err != nil {
		t.Fatal(err)
	}
	return m, publishDone{head: m.detailView.head, cachePath: m.cachePath, result: publish.Result{PR: *published.PR}}
}

func TestPublishDoneSurvivesGenerationAdvance(t *testing.T) {
	m, done := publishDoneModel(t)
	// A comment posted while the publish runs advances the generation.
	u, _ := m.Update(remoteCommentDone{generation: m.targetGeneration})
	m = u.(Model)
	if m.targetGeneration != 8 || !m.publishing {
		t.Fatalf("setup: generation=%d publishing=%v", m.targetGeneration, m.publishing)
	}

	u, cmd := m.Update(done)
	m = u.(Model)
	if m.publishing {
		t.Fatal("publishing stayed set after the publish finished")
	}
	if m.cache.LastPublishedManagedBodyHash != "new-hash" || m.cache.PR == nil || m.cache.PR.Body != "new" {
		t.Fatalf("published cache was not loaded: hash=%q pr=%#v", m.cache.LastPublishedManagedBodyHash, m.cache.PR)
	}
	if m.githubStatus != "PR updated: https://example/pr/7" {
		t.Fatalf("githubStatus = %q", m.githubStatus)
	}
	if cmd == nil || m.targetGeneration != 9 {
		t.Fatalf("publish did not schedule a refetch: generation=%d cmd=%v", m.targetGeneration, cmd)
	}
}

func TestPublishDoneReportsErrorAfterGenerationAdvance(t *testing.T) {
	m, done := publishDoneModel(t)
	m.targetGeneration++
	done.err = errors.New("git push: rejected")

	u, _ := m.Update(done)
	m = u.(Model)
	if m.publishing || m.status != "publish: git push: rejected" {
		t.Fatalf("publishing=%v status=%q", m.publishing, m.status)
	}
	if m.cache.LastPublishedManagedBodyHash != "old-hash" {
		t.Fatalf("failed publish replaced the cache: %q", m.cache.LastPublishedManagedBodyHash)
	}
}

func TestPublishDoneOnPRListLoadsCacheWithoutRefetch(t *testing.T) {
	m, done := publishDoneModel(t)
	// b returns to the list, which keeps the branch's cache for re-entry.
	m.screen, m.publishing = prListScreen, false
	m.targetGeneration++

	u, cmd := m.Update(done)
	m = u.(Model)
	if m.cache.LastPublishedManagedBodyHash != "new-hash" {
		t.Fatalf("published cache was not loaded: %q", m.cache.LastPublishedManagedBodyHash)
	}
	if cmd != nil || m.targetGeneration != 8 || m.githubStatus != "" {
		t.Fatalf("list screen was disturbed: generation=%d status=%q cmd=%v", m.targetGeneration, m.githubStatus, cmd)
	}
}

func TestPublishDoneForAnotherTargetIsDropped(t *testing.T) {
	m, done := publishDoneModel(t)
	done.head, done.cachePath = "other", filepath.Join(t.TempDir(), "github.json")

	u, cmd := m.Update(done)
	m = u.(Model)
	if !m.publishing || cmd != nil || m.cache.LastPublishedManagedBodyHash != "old-hash" || m.githubStatus != "" {
		t.Fatalf("stale publish touched the current target: publishing=%v hash=%q status=%q", m.publishing, m.cache.LastPublishedManagedBodyHash, m.githubStatus)
	}
}
