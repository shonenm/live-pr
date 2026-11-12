package tui

import (
	"testing"

	gh "github.com/shonenm/live-pr/v2/internal/github"
)

// Leaving a tab mid-fetch and coming back bumps the generation twice, so the
// tab's only in-flight response is dropped as stale. The return trip has to
// fetch again, or the tab stays on "fetching…" and r is ignored forever.
func TestPRViewRoundTripDuringFetchRequestsAgain(t *testing.T) {
	m := testModel()
	m.screen, m.prList.view, m.prList.state = prListScreen, assignedView, openPRListState
	assignedKey := prPageKey(assignedView, openPRListState, "")
	reviewKey := prPageKey(reviewRequestedView, openPRListState, "")
	m.prList.activePage = assignedKey
	// As New() leaves it: the first fetch for the active tab is in flight.
	m.prList.pages = map[string]prPageState{assignedKey: {loading: true}}
	m.prList.refreshing = true
	first := m.prList.generation

	m.prList.view = reviewRequestedView
	_ = m.applyPRViewState(0)
	second := m.prList.generation
	m.prList.view = assignedView
	if cmd := m.applyPRViewState(0); cmd == nil {
		t.Fatal("returning to a tab whose fetch was orphaned requested nothing")
	}
	if m.prList.generation == second || !m.prList.pages[assignedKey].loading {
		t.Fatalf("return trip = generation:%d page=%#v", m.prList.generation, m.prList.pages[assignedKey])
	}

	// Both superseded responses arrive and are dropped.
	m, _ = m.handlePRListRefreshed(prListRefreshed{generation: first, key: assignedKey, page: gh.PRPage{PRs: []gh.PR{{Number: 9}}, TotalCount: 1}})
	m, _ = m.handlePRListRefreshed(prListRefreshed{generation: second, key: reviewKey})
	if page := m.prList.pages[assignedKey]; !page.loading || page.loaded {
		t.Fatalf("stale response touched the active page: %#v", page)
	}

	// The re-request's response is the one that lands.
	m, _ = m.handlePRListRefreshed(prListRefreshed{generation: m.prList.generation, key: assignedKey, page: gh.PRPage{PRs: []gh.PR{{Number: 1, State: "OPEN"}}, TotalCount: 1}})
	if page := m.prList.pages[assignedKey]; page.loading || !page.loaded || m.prList.refreshing || len(page.prs) != 1 || page.prs[0].Number != 1 {
		t.Fatalf("after the fresh response = refreshing:%v page=%#v", m.prList.refreshing, page)
	}
}
