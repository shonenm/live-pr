package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prfilter"
)

func TestListStatusCloseUpdatesCachedBranchPR(t *testing.T) {
	m := testModel()
	m.screen, m.prList.view, m.prList.state = prListScreen, allPRsView, openPRListState
	m.currentBranch = "feature"
	m.navigator = gh.NewNavigatorCache()
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
	m.prList.activePage = prPageKey(allPRsView, openPRListState, "")
	stale := gh.PR{Number: 12, State: "OPEN", HeadRefName: "feature", Title: "x"}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &stale
	m.prList.pages = map[string]prPageState{m.prList.activePage: {prs: []gh.PR{stale}, total: 1, loaded: true, fresh: true}}

	// Closing via the status popup on the list screen must update the
	// branch cache too, or withLocalPR keeps re-injecting the stale copy.
	closed := stale
	closed.State = "CLOSED"
	u, _ := m.Update(prStatusDone{pr: closed, target: "closed"})
	m = u.(Model)
	if m.cache.PR.State != "CLOSED" {
		t.Fatalf("cached branch PR state = %q", m.cache.PR.State)
	}
	for _, pr := range m.prList.open {
		if pr.Number == 12 {
			t.Fatalf("closed PR re-injected into the open list: %#v", m.prList.open)
		}
	}
}

func TestPRStatusPopupOpensFromListAndDetail(t *testing.T) {
	for _, screen := range []screen{prListScreen, detailScreen} {
		m := testModel()
		m.screen = screen
		pr := gh.PR{Number: 12, State: "OPEN", Title: "status"}
		m.cache.PR = &pr
		m.prList.open = []gh.PR{pr}
		u, _ := m.Update(keyPress("s"))
		m = u.(Model)
		o, ok := m.overlay.(prStatusOverlay)
		if !ok {
			t.Fatalf("screen %v s did not open the status popup: %T", screen, m.overlay)
		}
		popup := ansi.Strip(o.render(m))
		if o.pr.Number != 12 || strings.Contains(popup, "\n   Open\n") || !strings.Contains(popup, "Close") || strings.Contains(popup, "Closed") || !strings.Contains(popup, "Draft") {
			t.Fatalf("screen %v status popup = %q", screen, popup)
		}
	}
}

func TestReopenKeepsDraftAndClosedFilterMatchesMerged(t *testing.T) {
	// Reopening a closed draft keeps its draftness in the optimistic update.
	pr := optimisticStatus(gh.PR{Number: 3, State: "CLOSED", IsDraft: true}, "open")
	if !pr.IsDraft || pr.State != "OPEN" {
		t.Fatalf("reopened draft = %#v", pr)
	}
	// The explicit draft -> open transition clears it.
	if pr := optimisticStatus(gh.PR{Number: 3, State: "OPEN", IsDraft: true}, "open"); pr.IsDraft {
		t.Fatalf("ready-for-review kept draft: %#v", pr)
	}

	// is:closed matches MERGED like GitHub search and matchesListState.
	if !prfilter.Matches(gh.PR{State: "MERGED"}, "is:closed", "") {
		t.Fatal("is:closed rejected a merged PR")
	}
	if prfilter.Matches(gh.PR{State: "OPEN"}, "is:closed", "") {
		t.Fatal("is:closed matched an open PR")
	}
}
