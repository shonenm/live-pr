package tui

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prfilter"
)

func TestCurrentPRSuppressesLocalPRRow(t *testing.T) {
	m := testModel()
	m.currentBranch = "feature/x"
	m.localAvailable = true
	pr := gh.PR{Number: 7, HeadRefName: "feature/x", BaseRefName: "main"}
	items := m.withLocalPR([]gh.PR{pr})
	if len(items) != 1 || items[0].Number != 7 {
		t.Fatalf("local row was not suppressed: %#v", items)
	}
	m.cache.PR = &pr
	items = m.withLocalPR(nil)
	if len(items) != 1 || items[0].Number != 7 {
		t.Fatalf("cached current PR was not listed: %#v", items)
	}
}

func TestLocalPRAppearsInEveryOpenView(t *testing.T) {
	m := testModel()
	local := gh.PR{}
	for _, view := range []prView{assignedView, reviewRequestedView, allPRsView, authoredView, needsMeView} {
		if !m.matchesView(local, view) {
			t.Fatalf("local PR missing from %s", m.viewName(view))
		}
	}
	if m.matchesView(local, closedPRsView) || !matchesListState(gh.PR{State: "MERGED", Number: 1}, closedPRListState) {
		t.Fatal("local/merged state routing is incorrect")
	}
}

func TestGitHubSemanticStatesUseMatchingStyles(t *testing.T) {
	if got := reviewSummary("APPROVED"); got != stGreenF.Render("review approved") {
		t.Fatalf("approved review = %q", got)
	}
	if got := reviewSummary("CHANGES_REQUESTED"); got != stRedF.Render("review changes requested") {
		t.Fatalf("changes-requested review = %q", got)
	}
	if text, style := mergeState(gh.PR{Number: 1, MergeStateStatus: "BLOCKED"}); style.Render(text) != stRedF.Render("blocked") {
		t.Fatalf("blocked merge = %q", style.Render(text))
	}
	if text, style := mergeState(gh.PR{Number: 1, Mergeable: "MERGEABLE", MergeStateStatus: "UNSTABLE"}); style.Render(text) != stGreenF.Render("⇄ mergeable") {
		t.Fatalf("unstable merge = %q", style.Render(text))
	}
	if text, style := checkState([]gh.PRCheck{{Status: "IN_PROGRESS"}}); style.Render(text) != stAttention.Render("◐ CI 1 pending") {
		t.Fatalf("pending CI = %q", style.Render(text))
	}
	if cDoneEmphasis != "#8957e5" {
		t.Fatalf("merged palette = %s", cDoneEmphasis)
	}
	if got := prStateBadgeColor("MERGED"); got != cDoneEmphasis {
		t.Fatalf("merged badge = %s", got)
	}
}

func TestSelectedPRRowPreservesSemanticStatusColors(t *testing.T) {
	m := testModel()
	m.list.SetWidth(140)
	pr := gh.PR{
		Number:           12,
		Title:            "status colors",
		State:            "OPEN",
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "CLEAN",
		Checks:           []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}},
		PreviewLoaded:    true,
		Additions:        8,
		Deletions:        3,
	}
	rows := strings.Join(m.renderPRRow(pr, true, ""), "\n")
	bg := lipgloss.Color(cSelectedBg)
	for _, want := range []string{
		stGreenF.Background(bg).Render("⇄ mergeable"),
		stGreenF.Background(bg).Render("✓ CI 1 passed"),
		stGreenF.Background(bg).Render("+8"),
		stRedF.Background(bg).Render("-3"),
	} {
		if !strings.Contains(rows, want) {
			t.Fatalf("selected PR row lost semantic style %q: %q", want, rows)
		}
	}
}

func TestPRRowCacheReusesUnselectedRowsAndInvalidatesWithFilters(t *testing.T) {
	m := testModel()
	m.list.SetWidth(120)
	m.prList.open = []gh.PR{{Number: 1, Title: "one"}, {Number: 2, Title: "two"}, {Number: 3, Title: "three"}}
	m.prList.stacks = prfilter.BuildStacks(m.prList.open)
	_, _ = m.buildPRListRows()
	if len(m.prList.rowCache) != 2 {
		t.Fatalf("initial cached rows = %d, want 2 unselected rows", len(m.prList.rowCache))
	}
	_, _ = m.buildPRListRows()
	if len(m.prList.rowCache) != 2 {
		t.Fatalf("stable render grew row cache to %d", len(m.prList.rowCache))
	}
	m.prList.cursor = 1
	_, _ = m.buildPRListRows()
	if len(m.prList.rowCache) != 3 {
		t.Fatalf("selection change cached rows = %d, want 3", len(m.prList.rowCache))
	}
	// Rows key on their full render inputs, so a data refresh keeps them;
	// eviction only happens past maxPRRowCacheEntries.
	m.applyPRFilters(0)
	if len(m.prList.rowCache) != 3 {
		t.Fatalf("data refresh dropped still-valid rows: %d", len(m.prList.rowCache))
	}
}

func TestPRListPreviewShowsConversationAndHealth(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.prList.open = []gh.PR{{
		Number: 15, Title: "navigator", Body: "## Summary\n\nPreview body", BaseRefName: "main", HeadRefName: "feature",
		Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", ReviewDecision: "APPROVED",
		ChangedFiles: 18, Additions: 1123, Deletions: 128, CommitCount: 5,
		Conversation: []gh.PRConversationComment{{Author: gh.PRUser{Login: "alice"}, Body: "Looks good", CreatedAt: "2026-08-08T00:00:00Z"}}, CommentCount: 1,
		Checks: []gh.PRCheck{{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}},
		Author: gh.PRUser{Login: "bob"}, Assignees: []gh.PRUser{{Login: "carol"}}, Labels: []gh.PRLabel{{Name: "feature", Color: "238636"}},
	}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	m = u.(Model)
	out := ansi.Strip(m.viewContent())
	for _, want := range []string{"description", "comment", "Summary", "Preview body", "@alice", "Looks good", "mergeable", "CI 1 passed", "18 files", "+1123", "-128", "5 commits", "1 comments", "author ● @bob", "feature", "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "Conversation top") {
		t.Fatalf("preview should use cards instead of a Conversation top heading: %q", out)
	}
	if m.list.Width() >= m.w || m.detail.Width() <= m.list.Width() {
		t.Fatalf("list preview layout = %d/%d total=%d", m.list.Width(), m.detail.Width(), m.w)
	}
}

func TestPRListPreviewShowsConflictAndFailedCI(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.prList.open = []gh.PR{{Number: 1, Mergeable: "CONFLICTING", MergeStateStatus: "DIRTY", Checks: []gh.PRCheck{{Conclusion: "FAILURE"}}}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	out := ansi.Strip(m.viewContent())
	if !strings.Contains(out, "conflicts") || !strings.Contains(out, "CI 1 failed") {
		t.Fatalf("health preview = %q", out)
	}
}

func TestCheckSummaryTreatsTerminalFailuresAsFailed(t *testing.T) {
	for _, conclusion := range []string{"STARTUP_FAILURE", "STALE"} {
		if got, _ := checkState([]gh.PRCheck{{Status: "COMPLETED", Conclusion: conclusion}}); !strings.Contains(got, "failed") {
			t.Fatalf("%s summary = %q", conclusion, got)
		}
	}
}

func TestLocalPRListEntryCarriesDiffStats(t *testing.T) {
	m := testModel()
	m.localAvailable, m.localTitle = true, "local"
	m.currentBranch, m.defaultBranch = "feature", "main"
	m.localStats = git.ChangeStats{Files: 3, Additions: 20, Deletions: 4}
	m.localCommitCount = 2
	items := m.withLocalPR(nil)
	if len(items) != 1 || items[0].ChangedFiles != 3 || items[0].Additions != 20 || items[0].Deletions != 4 || items[0].CommitCount != 2 {
		t.Fatalf("local PR metadata = %#v", items)
	}
}

func TestPRViewOrderDefaultsToAssigned(t *testing.T) {
	var m Model
	if m.prList.view != assignedView {
		t.Fatalf("default view = %v", m.prList.view)
	}
	want := []prView{assignedView, reviewRequestedView, allPRsView, authoredView, needsMeView, closedPRsView}
	for i, view := range want {
		if prView(i) != view {
			t.Fatalf("view order[%d] = %v, want %v", i, prView(i), view)
		}
	}
}

func TestPRSavedViewsUseViewerMetadata(t *testing.T) {
	m := testModel()
	m.viewerLogin = "me"
	m.navigator.PRs = []gh.PR{
		{Number: 1, Author: gh.PRUser{Login: "me"}},
		{Number: 2, Assignees: []gh.PRUser{{Login: "me"}}},
		{Number: 3, ReviewRequests: []gh.PRUser{{Login: "me"}}},
		{Number: 4, Author: gh.PRUser{Login: "other"}},
	}
	want := map[prView][]int{
		allPRsView:          {1, 2, 3, 4},
		reviewRequestedView: {3},
		assignedView:        {2},
		authoredView:        {1},
		needsMeView:         {2, 3},
	}
	for view, numbers := range want {
		m.prList.view = view
		m.applyPRFilters(0)
		got := make([]int, len(m.prList.open))
		for i := range m.prList.open {
			got[i] = m.prList.open[i].Number
		}
		if !reflect.DeepEqual(got, numbers) {
			t.Fatalf("view %s = %v, want %v", m.viewName(view), got, numbers)
		}
	}
}

func TestPRViewSearchAndLocalOnlyFilters(t *testing.T) {
	// GitHub's issue search cannot evaluate OR groups — it matches them as
	// free text and returns nothing — so they stay out of the server query
	// and are applied locally instead.
	query := testModel().prViewSearch(needsMeView, openPRListState, "label:bug ci:failed merge:conflicting")
	if query != "is:open label:bug" {
		t.Fatalf("needs-me query = %q", query)
	}
}

// A view whose query GitHub cannot evaluate must still list and count the
// right pull requests: the server returns a superset and the tab narrows it.
func TestOrGroupViewFiltersAndCountsLocally(t *testing.T) {
	m := testModel()
	m.screen, m.viewerLogin = prListScreen, "me"
	m.views = []config.View{{Name: "Needs me", Query: "(review-requested:@me OR assignee:@me OR author:@me)"}}
	m.prList.view, m.prList.state = 0, openPRListState
	m.prList.activePage = prPageKey(0, openPRListState, "")
	m.navigator = gh.NewNavigatorCache()

	// What reaches GitHub carries no group, so the search is answerable.
	if got := m.prViewSearch(0, openPRListState, ""); got != "is:open" {
		t.Fatalf("server query = %q", got)
	}

	mine := gh.PR{Number: 1, State: "OPEN", Author: gh.PRUser{Login: "me"}}
	assigned := gh.PR{Number: 2, State: "OPEN", Assignees: []gh.PRUser{{Login: "me"}}}
	reviewing := gh.PR{Number: 3, State: "OPEN", ViewerReviewRequested: true}
	other := gh.PR{Number: 4, State: "OPEN", Author: gh.PRUser{Login: "you"}}
	superset := []gh.PR{mine, assigned, reviewing, other}
	m.prList.pages = map[string]prPageState{m.prList.activePage: {prs: superset, total: len(superset), loaded: true, fresh: true}}
	m.navigator.PRs = superset

	m.applyPRFilters(0)
	var listed []int
	for _, pr := range m.prList.open {
		listed = append(listed, pr.Number)
	}
	if !reflect.DeepEqual(listed, []int{1, 2, 3}) {
		t.Fatalf("listed = %v, want the three that involve me", listed)
	}
	// The server total counts the superset, so the tab counts loaded rows.
	if got := m.viewCount(0); got != 3 {
		t.Fatalf("view count = %d, want 3", got)
	}
}

func TestPRPaginationAppendsOnceAndCachesView(t *testing.T) {
	m := testModel()
	m.screen, m.prList.view, m.prList.state = prListScreen, allPRsView, openPRListState
	m.prList.generation = 3
	m.prList.activePage = prPageKey(allPRsView, openPRListState, "")
	m.prList.pages = map[string]prPageState{m.prList.activePage: {prs: []gh.PR{{Number: 1, Title: "old"}}, total: 3, endCursor: "C1", hasNext: true, loaded: true, fresh: true}}
	m.navigatorPath = filepath.Join(t.TempDir(), "navigator.json")
	m.applyPRFilters(1)
	if cmd := m.requestPRPage(false); cmd == nil || !m.prList.pages[m.prList.activePage].loading {
		t.Fatal("next page was not scheduled")
	}
	if cmd := m.requestPRPage(false); cmd != nil {
		t.Fatal("duplicate in-flight page was scheduled")
	}
	u, _ := m.Update(prListRefreshed{
		generation: 3,
		key:        m.prList.activePage,
		appendPage: true,
		page: gh.PRPage{
			PRs:        []gh.PR{{Number: 1, Title: "updated"}, {Number: 2, Title: "new"}},
			TotalCount: 2,
			PageInfo:   gh.PageInfo{EndCursor: "C2"},
		},
	})
	m = u.(Model)
	page := m.prList.pages[m.prList.activePage]
	if page.loading || page.hasNext || len(page.prs) != 2 || page.prs[0].Title != "updated" || page.prs[1].Number != 2 {
		t.Fatalf("appended page = %#v", page)
	}
	cached, meta, ok := m.navigator.View("All")
	if !ok || meta.TotalCount != 2 || len(cached) != 2 {
		t.Fatalf("cached page = %#v meta=%#v ok=%v", cached, meta, ok)
	}
}

func TestPRViewSwitchUsesFreshPageWithoutRequest(t *testing.T) {
	m := testModel()
	m.screen, m.prList.view, m.prList.state = prListScreen, assignedView, openPRListState
	m.prList.activePage = prPageKey(assignedView, openPRListState, "")
	reviewKey := prPageKey(reviewRequestedView, openPRListState, "")
	m.prList.pages = map[string]prPageState{
		m.prList.activePage: {prs: []gh.PR{{Number: 1}}, total: 1, loaded: true, fresh: true},
		reviewKey:           {prs: []gh.PR{{Number: 2}}, total: 1, loaded: true, fresh: true},
	}
	m.prList.view = reviewRequestedView
	_ = m.applyPRViewState(0)
	if m.prList.activePage != reviewKey || m.prList.refreshing || m.prList.pages[reviewKey].loading || len(m.prList.open) != 1 || m.prList.open[0].Number != 2 {
		t.Fatalf("fresh view switch = key:%q refreshing:%v page=%#v prs=%#v", m.prList.activePage, m.prList.refreshing, m.prList.pages[reviewKey], m.prList.open)
	}
}

func TestPRPaginationStartsWhenNavigationReachesLastRow(t *testing.T) {
	m := testModel()
	m.screen, m.prList.view, m.prList.state = prListScreen, allPRsView, openPRListState
	m.prList.activePage = prPageKey(allPRsView, openPRListState, "")
	m.prList.pages = map[string]prPageState{m.prList.activePage: {prs: []gh.PR{{Number: 1}, {Number: 2}}, total: 3, endCursor: "C1", hasNext: true, loaded: true, fresh: true}}
	m.applyPRFilters(1)
	if cmd := m.moveCursorTo(1); cmd == nil || !m.prList.pages[m.prList.activePage].loading {
		t.Fatal("last-row navigation did not request the next page")
	}
}

func TestPRListLoadsClosedFromView(t *testing.T) {
	m := testModel()
	m.screen, m.viewerLogin = prListScreen, "me"
	m.navigator.FetchedStates = map[string]bool{"OPEN": true}
	m.navigator.PRs = []gh.PR{{Number: 1, State: "OPEN", Title: "open", Assignees: []gh.PRUser{{Login: "me"}}}}
	m.applyPRFilters(0)
	if len(m.prList.open) != 1 || m.prList.open[0].Number != 1 {
		t.Fatalf("default state = %#v", m.prList.open)
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	m.prList.view = needsMeView
	u, _ = m.Update(keyPress("]"))
	m = u.(Model)
	if m.prList.view != closedPRsView || m.prList.state != closedPRListState || !m.prList.refreshing || len(m.prList.open) != 0 {
		t.Fatalf("closed view switch = view:%v state:%v refreshing:%v prs:%#v", m.prList.view, m.prList.state, m.prList.refreshing, m.prList.open)
	}
	u, _ = m.Update(prListRefreshed{generation: m.prList.generation, key: m.prList.activePage, page: gh.PRPage{ViewerLogin: "me", PRs: []gh.PR{{Number: 2, State: "CLOSED", Title: "closed"}}, TotalCount: 1}})
	m = u.(Model)
	m.sync()
	if len(m.prList.open) != 1 || m.prList.open[0].Number != 2 || len(m.navigator.PRs) != 2 || m.viewCount(assignedView) != 1 || !strings.Contains(ansi.Strip(m.buildPRList()), "closed") {
		t.Fatalf("closed PR list/cache = visible:%#v cached:%#v assigned:%d", m.prList.open, m.navigator.PRs, m.viewCount(assignedView))
	}
	if m.keys.Merge.Enabled() || m.keys.Close.Enabled() {
		t.Fatal("closed PR actions must be disabled")
	}
	u, _ = m.Update(keyPress("]"))
	m = u.(Model)
	if m.prList.view != assignedView || !m.prList.refreshing || len(m.prList.open) != 1 || m.prList.open[0].Number != 1 {
		t.Fatalf("cache-first assigned view = view:%v refreshing:%v prs:%#v", m.prList.view, m.prList.refreshing, m.prList.open)
	}
}

func TestClosedPRsDoNotRenderAsStacks(t *testing.T) {
	m := testModel()
	m.prList.state = closedPRListState
	m.prList.filtered = []gh.PR{
		{Number: 1, State: "CLOSED", HeadRefName: "one", BaseRefName: "main"},
		{Number: 2, State: "CLOSED", HeadRefName: "two", BaseRefName: "one"},
	}
	m.applyPRFilters(0)
	for _, stack := range m.prList.stacks {
		if len(stack.Entries) != 1 {
			t.Fatalf("closed stack = %#v", stack)
		}
	}
}

func TestPRListLoadsClosedFromSearch(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator.FetchedStates = map[string]bool{"OPEN": true}
	m.navigator.PRs = []gh.PR{{Number: 1, State: "OPEN", Title: "open"}}
	m.applyPRFilters(0)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(keyPress("/"))
	m = u.(Model)
	u, _ = m.Update(keyPress("is:closed"))
	m = u.(Model)
	u, _ = m.Update(keyPress("enter"))
	m = u.(Model)
	if m.prList.view != allPRsView || m.prList.state != closedPRListState || !m.prList.refreshing || len(m.prList.open) != 0 {
		t.Fatalf("closed search = view:%v state:%v refreshing:%v prs:%#v", m.prList.view, m.prList.state, m.prList.refreshing, m.prList.open)
	}
	u, _ = m.Update(prListRefreshed{generation: m.prList.generation, key: m.prList.activePage, page: gh.PRPage{PRs: []gh.PR{{Number: 2, State: "CLOSED", Title: "closed"}}, TotalCount: 1}})
	m = u.(Model)
	u, _ = m.Update(keyPress("esc"))
	m = u.(Model)
	if m.prList.filterQuery != "" || m.prList.state != openPRListState || !m.prList.refreshing || len(m.prList.open) != 1 || m.prList.open[0].Number != 1 {
		t.Fatalf("cleared closed search = query:%q state:%v refreshing:%v prs:%#v", m.prList.filterQuery, m.prList.state, m.prList.refreshing, m.prList.open)
	}
}

func TestPRListHeaderShowsRepositoryAtWideAndNarrowWidths(t *testing.T) {
	for _, width := range []int{50, 120} {
		m := testModel()
		m.repository, m.w = "acme/project", width
		if out := ansi.Strip(m.renderPRListHeader()); !strings.Contains(out, "acme/project") {
			t.Fatalf("width %d header missing repository: %q", width, out)
		}
	}
}

func TestPRFilteringDoesNotMutateNavigatorCache(t *testing.T) {
	m := testModel()
	m.currentBranch, m.defaultBranch = "feature/local", "main"
	m.localAvailable = true
	m.navigator.PRs = []gh.PR{{Number: 1, HeadRefName: "one"}, {Number: 2, HeadRefName: "two"}}
	want := append([]gh.PR(nil), m.navigator.PRs...)
	m.applyPRFilters(0)
	if !reflect.DeepEqual(m.navigator.PRs, want) {
		t.Fatalf("display filtering mutated navigator cache: got=%#v want=%#v", m.navigator.PRs, want)
	}
}

func TestPRStackRenderingAndCollapse(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.navigator.PRs = []gh.PR{
		{Number: 3, Title: "UI", BaseRefName: "stack/api", HeadRefName: "stack/ui", MergeStateStatus: "DIRTY", Checks: []gh.PRCheck{{Status: "IN_PROGRESS"}}},
		{Number: 2, Title: "API", BaseRefName: "stack/model", HeadRefName: "stack/api", Checks: []gh.PRCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}},
		{Number: 1, Title: "Model", BaseRefName: "main", HeadRefName: "stack/model"},
	}
	m.applyPRFilters(3)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	plain := ansi.Strip(m.buildPRList())
	for _, want := range []string{"#1 · 3 PRs", "├ #1", "├ #2", "└ #3"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("stack render missing %q: %q", want, plain)
		}
	}
	if !strings.Contains(plain, "CI 1 pending") || !strings.Contains(plain, "conflicts") {
		t.Fatalf("PR row state missing: %q", plain)
	}
	if strings.Contains(plain, "3 PRs ·") {
		t.Fatalf("stack header leaked aggregate PR state: %q", plain)
	}
	u, _ = m.Update(keyPress("space"))
	m = u.(Model)
	if len(m.prList.open) != 1 || m.prList.open[0].Number != 1 || !m.prList.collapsedStacks[m.prList.stacks[0].ID] {
		t.Fatalf("collapsed stack = prs:%#v collapsed:%#v", m.prList.open, m.prList.collapsedStacks)
	}
	if !strings.Contains(ansi.Strip(m.buildPRList()), "▸ #1") {
		t.Fatalf("collapsed header = %q", ansi.Strip(m.buildPRList()))
	}
	u, _ = m.Update(keyPress("space"))
	m = u.(Model)
	if len(m.prList.open) != 3 || m.prList.collapsedStacks[m.prList.stacks[0].ID] {
		t.Fatalf("expanded stack = prs:%#v collapsed:%#v", m.prList.open, m.prList.collapsedStacks)
	}
}

func TestPRListScrollTracksRenderedRows(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	for i := 1; i <= 20; i++ {
		m.prList.open = append(m.prList.open, gh.PR{Number: i, Title: "PR"})
	}
	m.prList.stacks = prfilter.BuildStacks(m.prList.open)
	m.prList.cursor = 10
	u, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = u.(Model)
	if m.list.YOffset() < 20 {
		t.Fatalf("list offset did not follow rendered rows: %d", m.list.YOffset())
	}
}

func TestPRRowShowsLabelPills(t *testing.T) {
	m := testModel()
	m.list.SetWidth(160)
	pr := gh.PR{Number: 5, State: "OPEN", Title: "Labelled", BaseRefName: "main", HeadRefName: "labels",
		Labels: []gh.PRLabel{{Name: "bug", Color: "d73a4a"}, {Name: "docs", Color: "fef2c0"}, {Name: "ui", Color: "238636"}, {Name: "infra", Color: "0e8a16"}}}
	row := ansi.Strip(strings.Join(m.renderPRRow(pr, false, ""), "\n"))
	for _, want := range []string{" bug ", " docs ", " ui ", "+1"} {
		if !strings.Contains(row, want) {
			t.Fatalf("label pill %q missing from row: %q", want, row)
		}
	}
	if strings.Contains(row, "infra") {
		t.Fatalf("fourth label should collapse into +N: %q", row)
	}
	// A PR without labels keeps its meta line untouched: branch info stays last.
	bare := gh.PR{Number: 6, State: "OPEN", Title: "Bare", BaseRefName: "main", HeadRefName: "bare"}
	bareRows := m.renderPRRow(bare, false, "")
	if meta := strings.TrimRight(ansi.Strip(bareRows[1]), " "); !strings.HasSuffix(meta, "main ← bare") {
		t.Fatalf("unlabelled meta line changed: %q", meta)
	}
	// The synthetic local PR row (Number == 0) never shows pills.
	local := gh.PR{Number: 0, Title: "Local", BaseRefName: "main", HeadRefName: "wip",
		Labels: []gh.PRLabel{{Name: "bug", Color: "d73a4a"}}}
	if localRow := ansi.Strip(strings.Join(m.renderPRRow(local, false, ""), "\n")); strings.Contains(localRow, "bug") {
		t.Fatalf("local PR row should not render pills: %q", localRow)
	}
}

func TestUserIconsAppearAcrossPRSurfaces(t *testing.T) {
	m := testModel()
	m.w = 160
	m.list.SetWidth(70)
	m.detail.SetWidth(80)
	pr := gh.PR{Number: 7, State: "OPEN", Title: "icons", BaseRefName: "main", HeadRefName: "icons", Author: gh.PRUser{Login: "alice"}, Assignees: []gh.PRUser{{Login: "bob"}}, PreviewLoaded: true}
	row := ansi.Strip(strings.Join(m.renderPRRow(pr, false, ""), "\n"))
	preview := ansi.Strip(func() string { m.prList.open = []gh.PR{pr}; return m.buildPRPreview() }())
	m.cache.PR = &pr
	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(row, "● @alice") {
		t.Fatalf("row user icon missing: %q", row)
	}
	// The author leads the meta line; the state word would only repeat the
	// colored glyph on the title line.
	if strings.Contains(row, "open") {
		t.Fatalf("row still spells out the state: %q", row)
	}
	localRow := ansi.Strip(strings.Join(m.renderPRRow(gh.PR{Number: 0, Title: "Local", BaseRefName: "main", HeadRefName: "wip"}, false, ""), "\n"))
	if !strings.Contains(localRow, "local") {
		t.Fatalf("authorless local row lost its state word: %q", localRow)
	}
	if !strings.Contains(preview, "author ● @alice") || strings.Contains(preview, "assigned") {
		t.Fatalf("preview should show the author and no assignees: %q", preview)
	}
	if !strings.Contains(header, "assigned ● @bob") {
		t.Fatalf("header user icon missing: %q", header)
	}
}

func TestRecomputeViewCountsDoesNotDoubleCountCachedViews(t *testing.T) {
	m := testModel()
	m.prList.all = []gh.PR{{Number: 1, State: "OPEN"}, {Number: 2, State: "OPEN"}}
	// A cached page holds the exact server-side total for the all view.
	m.prList.pages = map[string]prPageState{
		prPageKey(allPRsView, openPRListState, ""): {total: 5, loaded: true},
	}

	m.recomputeViewCounts(prPageState{}, false)

	if m.prList.viewCounts[allPRsView] != 5 {
		t.Fatalf("all view count = %d, want cached total 5", m.prList.viewCounts[allPRsView])
	}
	// Views without a cached page still fall back to counting prList.all.
	if m.prList.viewCounts[closedPRsView] != 0 || !m.prList.viewCountKnown[closedPRsView] {
		t.Fatalf("closed view = %d known=%v", m.prList.viewCounts[closedPRsView], m.prList.viewCountKnown[closedPRsView])
	}
}

func TestApplyPRFiltersKeepsRowCacheWithinBound(t *testing.T) {
	m := testModel()
	m.navigator = gh.NewNavigatorCache()
	m.prList.rowCache = map[prRowCacheKey][]string{{number: 1}: {"row"}}
	m.applyPRFilters(0)
	if len(m.prList.rowCache) != 1 {
		t.Fatal("data refresh dropped still-valid row renders")
	}
	for i := 0; i <= maxPRRowCacheEntries; i++ {
		m.prList.rowCache[prRowCacheKey{number: i}] = []string{"row"}
	}
	m.applyPRFilters(0)
	if len(m.prList.rowCache) != 0 {
		t.Fatalf("over-cap cache not evicted: %d", len(m.prList.rowCache))
	}
}

func TestConfiguredViewsDriveTabsSearchAndCounts(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.viewerLogin = "me"
	m.views = []config.View{
		{Name: "Mine", Query: "author:@me"},
		{Name: "Bugs", Query: "label:bug"},
		{Name: "Done", Query: "is:closed"},
	}
	mine := gh.PR{Number: 1, State: "OPEN", Author: gh.PRUser{Login: "me"}}
	bug := gh.PR{Number: 2, State: "OPEN", Author: gh.PRUser{Login: "you"}, Labels: []gh.PRLabel{{Name: "bug"}}}
	done := gh.PR{Number: 3, State: "MERGED", Author: gh.PRUser{Login: "you"}}
	m.navigator = gh.NewNavigatorCache()
	m.navigator.PRs = []gh.PR{mine, bug, done}
	m.prList.all = m.navigator.PRs

	// The tab's own query decides membership and its open/closed bucket.
	for _, tc := range []struct {
		view prView
		pr   gh.PR
		want bool
	}{{0, mine, true}, {0, bug, false}, {1, bug, true}, {1, mine, false}, {2, done, true}} {
		if got := m.matchesView(tc.pr, tc.view); got != tc.want {
			t.Fatalf("%s matches #%d = %v, want %v", m.viewName(tc.view), tc.pr.Number, got, tc.want)
		}
	}
	if m.standardPRListState(2) != closedPRListState || m.standardPRListState(0) != openPRListState {
		t.Fatal("view state not derived from the query")
	}

	// The search sends the tab's query alongside the state and user filter.
	if got := m.prViewSearch(1, openPRListState, "ci:failed rebase"); got != "is:open label:bug rebase" {
		t.Fatalf("search = %q", got)
	}

	// Counts and the tab bar follow the configured names and order.
	m.recomputeViewCounts(prPageState{}, false)
	if m.viewCount(0) != 1 || m.viewCount(1) != 1 || m.viewCount(2) != 1 {
		t.Fatalf("counts = %v", m.prList.viewCounts)
	}
	m.w = 200
	header := ansi.Strip(m.renderPRListHeader())
	if !strings.Contains(header, "[ Mine 1 ]") || !strings.Contains(header, "[ Done 1 ]") {
		t.Fatalf("tab bar = %q", header)
	}
	if strings.Contains(header, "Assigned") {
		t.Fatal("tab bar still shows a built-in view")
	}

	// [ ] cycle over the configured list, not a fixed count of six.
	m.prList.view = 2
	if next := m.stepView(1); next != 0 {
		t.Fatalf("wrap-around landed on %d", next)
	}
	if prev := m.stepView(-1); prev != 1 {
		t.Fatalf("previous view = %d", prev)
	}
}

func TestPRRowMarksCheckedOutBranch(t *testing.T) {
	m := testModel()
	m.list.SetWidth(140)
	m.currentBranch = "feature"
	current := gh.PR{Number: 12, State: "OPEN", Title: "Current", BaseRefName: "main", HeadRefName: "feature"}
	if row := ansi.Strip(strings.Join(m.renderPRRow(current, false, ""), "\n")); !strings.Contains(row, "⎇ checked out") {
		t.Fatalf("checked-out badge missing: %q", row)
	}
	other := gh.PR{Number: 13, State: "OPEN", Title: "Other", BaseRefName: "main", HeadRefName: "other"}
	if row := ansi.Strip(strings.Join(m.renderPRRow(other, false, ""), "\n")); strings.Contains(row, "⎇ checked out") {
		t.Fatalf("badge leaked onto a non-current row: %q", row)
	}
	// The synthetic local row already reads "local"; no badge on top.
	local := gh.PR{Number: 0, Title: "Local", BaseRefName: "main", HeadRefName: "feature"}
	if row := ansi.Strip(strings.Join(m.renderPRRow(local, false, ""), "\n")); strings.Contains(row, "⎇ checked out") {
		t.Fatalf("badge rendered on the local row: %q", row)
	}
}

func TestPRRowShowsReviewDecisionBadge(t *testing.T) {
	m := testModel()
	m.list.SetWidth(140)
	for decision, want := range map[string]string{
		"APPROVED":          stGreenF.Render("✓ approved"),
		"CHANGES_REQUESTED": stRedF.Render("± changes"),
		"REVIEW_REQUIRED":   stAttention.Render("◌ review"),
	} {
		pr := gh.PR{Number: 5, State: "OPEN", Title: "badge", BaseRefName: "main", HeadRefName: "head", ReviewDecision: decision}
		if row := strings.Join(m.renderPRRow(pr, false, ""), "\n"); !strings.Contains(row, want) {
			t.Fatalf("%s badge missing %q: %q", decision, want, row)
		}
	}
	// No decision (older cached rows) renders no badge at all.
	bare := gh.PR{Number: 6, State: "OPEN", Title: "bare", BaseRefName: "main", HeadRefName: "head"}
	row := ansi.Strip(strings.Join(m.renderPRRow(bare, false, ""), "\n"))
	for _, unwanted := range []string{"◌ review", "✓ approved", "± changes"} {
		if strings.Contains(row, unwanted) {
			t.Fatalf("badge %q rendered without a decision: %q", unwanted, row)
		}
	}
}

func TestPRRowKeepsAuthorVisibleWithLabels(t *testing.T) {
	m := testModel()
	m.list.SetWidth(160)
	pr := gh.PR{Number: 8, State: "OPEN", Title: "authored", BaseRefName: "main", HeadRefName: "feature",
		Author: gh.PRUser{Login: "alice"},
		Labels: []gh.PRLabel{{Name: "bug", Color: "d73a4a"}, {Name: "docs", Color: "fef2c0"}, {Name: "infra", Color: "0e8a16"}}}
	meta := ansi.Strip(m.renderPRRow(pr, false, "")[1])
	author, label := strings.Index(meta, "@alice"), strings.Index(meta, " bug ")
	if author < 0 || label < 0 || author > label {
		t.Fatalf("author should precede label pills: %q", meta)
	}
	// A width that cuts into the pills still leaves the author visible.
	m.list.SetWidth(lipgloss.Width(strings.TrimRight(meta, " ")) - 4)
	narrow := ansi.Strip(m.renderPRRow(pr, false, "")[1])
	if !strings.Contains(narrow, "@alice") || strings.Contains(narrow, "infra") {
		t.Fatalf("narrow row lost the author before the pills: %q", narrow)
	}
}

func TestPRPreviewGroupsCommentsWithReviewState(t *testing.T) {
	m := testModel()
	m.w = 160
	m.list.SetWidth(70)
	m.detail.SetWidth(80)
	pr := gh.PR{Number: 9, State: "OPEN", Title: "preview", BaseRefName: "main", HeadRefName: "feature",
		ReviewDecision: "APPROVED", CommentCount: 3, ChangedFiles: 2, Additions: 10, Deletions: 4, CommitCount: 1, PreviewLoaded: true}
	m.prList.open = []gh.PR{pr}
	lines := strings.Split(ansi.Strip(m.buildPRPreview()), "\n")
	var conversation, size string
	for _, line := range lines {
		if strings.Contains(line, "review approved") {
			conversation = line
		}
		if strings.Contains(line, "files") {
			size = line
		}
	}
	if !strings.Contains(conversation, "3 comments") {
		t.Fatalf("comments left the review line: %#v", lines)
	}
	if size == "" || strings.Contains(size, "comments") {
		t.Fatalf("size line should carry only diff stats: %q", size)
	}
	// Without a review decision the comment count still renders.
	pr.ReviewDecision = ""
	m.prList.open = []gh.PR{pr}
	if out := ansi.Strip(m.buildPRPreview()); !strings.Contains(out, "3 comments") {
		t.Fatalf("comments disappeared without a review decision: %q", out)
	}
}

func TestPRNumberCarriesStateColor(t *testing.T) {
	m := testModel()
	m.list.SetWidth(120)
	for _, test := range []struct {
		name  string
		pr    gh.PR
		style lipgloss.Style
	}{
		{"open", gh.PR{Number: 7, State: "OPEN", Title: "open one"}, stGreenF},
		{"merged", gh.PR{Number: 8, State: "MERGED", Title: "merged one"}, lipgloss.NewStyle().Foreground(lipgloss.Color(cDoneEmphasis))},
		{"closed", gh.PR{Number: 9, State: "CLOSED", Title: "closed one"}, stRedF},
		{"draft", gh.PR{Number: 10, State: "OPEN", IsDraft: true, Title: "draft one"}, stMuted},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := strings.Join(m.renderPRRow(test.pr, false, ""), "\n")
			if want := test.style.Render(fmt.Sprintf("#%d", test.pr.Number)); !strings.Contains(row, want) {
				t.Fatalf("PR number not styled like its state glyph: %q", row)
			}
		})
	}
	// The stack prefix draws the graph, not the state, so it stays muted.
	stacked := ansi.Strip(strings.Join(m.renderPRRow(gh.PR{Number: 11, State: "OPEN", Title: "child"}, false, "└ "), "\n"))
	if !strings.Contains(stacked, "└ #11 child") {
		t.Fatalf("stack prefix layout changed: %q", stacked)
	}
}
