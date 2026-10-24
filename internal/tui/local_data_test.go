package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
)

func TestLocalLoadReportsPartialFailures(t *testing.T) {
	m, st := modeFixture(t)
	cache := m.cache.Clone()
	cache.PR.HeadRefOID = "missing-head"
	cache.PR.BaseRefOID = "missing-base"
	m.w = 160
	if err := os.Remove(st.Conclusion()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(st.Conclusion(), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := loadLocalData(context.Background(), st, cache, nil, "missing-base")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"timeline sync", "diff statistics", "revision distance", "conclusion"} {
		if !strings.Contains(data.warning, label) {
			t.Errorf("missing %s warning: %s", label, data.warning)
		}
	}
	if !data.incomplete || data.revisionRelation != git.RevisionUnknown || data.statsErr == nil || data.revisionErr == nil {
		t.Fatalf("failure presented as success: %+v", data)
	}
	m.applyLocal(st, data)
	if !strings.Contains(m.renderHeader(), "diff statistics unavailable") || !strings.Contains(m.dataModeContext(), "revision distance unavailable") {
		t.Fatal("missing unavailable labels")
	}
}

func TestLocalPartialFailurePreservesSameTargetData(t *testing.T) {
	m, st := modeFixture(t)
	m.detailView.summary = "keep summary"
	m.localStats = git.ChangeStats{Files: 3, Additions: 5}
	m.detailView.files = []git.ChangedFile{{Path: "keep.go"}}
	data, err := loadLocalData(context.Background(), st, m.cache.Clone(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("unavailable")
	data.stats, data.files, data.conclusion = git.ChangeStats{}, nil, ""
	data.statsErr, data.filesErr, data.conclusionErr = failure, failure, failure
	data.incomplete, data.warning = true, "fixture failure"
	m.applyLocal(st, data)
	if m.localStats.Files != 3 || len(m.detailView.files) != 1 || m.detailView.summary != "keep summary" {
		t.Fatal("same-target valid data lost")
	}
	data.cache = gh.NewCache("different")
	m.applyLocal(st, data)
	if m.localStats.Files != 0 || len(m.detailView.files) != 0 || m.detailView.summary != "" {
		t.Fatal("different target inherited stale data")
	}
}

func TestRefreshUsesLocalLoadErrorPolicy(t *testing.T) {
	m, st := modeFixture(t)
	m.cache = gh.NewCache(st.Branch)
	startup, err := loadLocalData(context.Background(), st, m.cache, nil, "missing-base")
	if err != nil {
		t.Fatal(err)
	}
	msg := m.resolveBase("missing-base", nil, "")().(baseResolved)
	if msg.err != nil || msg.local == nil {
		t.Fatalf("refresh = %+v", msg)
	}
	if startup.warning != msg.local.warning || !msg.local.incomplete {
		t.Fatalf("startup/refresh mismatch: %q / %q", startup.warning, msg.local.warning)
	}
	next, _ := m.handleBaseResolved(msg)
	if !strings.Contains(next.status, "local data is incomplete") || next.refreshing {
		t.Fatalf("refresh error hidden: %q", next.status)
	}
}

func TestAutomaticPRCacheErrorKeepsMatchingMemory(t *testing.T) {
	m, st := modeFixture(t)
	m.screen, m.autoOpenCurrent = prListScreen, true
	m.cache.Comments = []gh.Comment{{Body: "keep comment"}}
	if err := os.Mkdir(st.GitHubCache(), 0o700); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.handleCurrentBranchPRLoaded(currentBranchPRLoaded{pr: *m.cache.PR})
	if !strings.Contains(next.status, "GitHub cache ignored") {
		t.Fatalf("cache error hidden: %q", next.status)
	}
	batch := cmd().(tea.BatchMsg)
	loaded := batch[1]().(localLoaded)
	if loaded.err != nil || len(loaded.data.cache.Comments) != 1 || loaded.data.cache.Comments[0].Body != "keep comment" {
		t.Fatalf("memory cache lost: %+v %v", loaded.data.cache, loaded.err)
	}
}
