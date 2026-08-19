package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	gh "github.com/shonenm/live-pr/internal/github"
)

func TestReservedReviewKeysStayWithLivePR(t *testing.T) {
	if !reservedReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}) {
		t.Fatal("q should stay with live-pr")
	}
	if reservedReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) {
		t.Fatal("j should be forwarded to the reviewer")
	}
}

func TestStaticDiffFocusAndQReturnToConversation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.diffCommand = ""
	m.ready = true

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = u.(Model)
	if m.focusDiff || !m.focusExplorer {
		t.Fatalf("l focus = diff:%v explorer:%v", m.focusDiff, m.focusExplorer)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = u.(Model)
	if m.focusDiff || !m.focusExplorer {
		t.Fatalf("second l focus = diff:%v explorer:%v", m.focusDiff, m.focusExplorer)
	}
	// q from the explorer should quit (Tab cycles focus instead).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q from explorer should quit")
	}
}

func TestPRListNavigationAndRefresh(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.openPRs = []gh.PR{{Number: 1, Title: "first"}, {Number: 2, Title: "second"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	if out := ansi.Strip(m.View()); !strings.Contains(out, "Pull requests") || !strings.Contains(out, "#1") {
		t.Fatalf("PR list missing content: %q", out)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	if m.prCursor != 1 {
		t.Fatalf("PR cursor = %d", m.prCursor)
	}
	m.listRefreshing = false
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = u.(Model)
	if cmd == nil || !m.listRefreshing {
		t.Fatal("r should explicitly refresh the PR list")
	}
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q should quit the PR list")
	}
}

func TestPRListVimNavigationAndNarrowLayout(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.openPRs = make([]gh.PR, 20)
	for i := range m.openPRs {
		m.openPRs[i] = gh.PR{Number: i + 1, Title: fmt.Sprintf("PR %d", i+1)}
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = u.(Model)
	if m.list.Width+m.detail.Width+3 > m.w {
		t.Fatalf("narrow layout overflow: list=%d detail=%d width=%d", m.list.Width, m.detail.Width, m.w)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	if m.prCursor != 0 {
		t.Fatalf("gg did not move PR list to top: %d", m.prCursor)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = u.(Model)
	if m.prCursor != len(m.openPRs)-1 {
		t.Fatalf("G did not move PR list to bottom: %d", m.prCursor)
	}
	m.detail.Height = 3
	m.detail.SetContent(strings.Repeat("preview\n", 20))
	m.detail.GotoBottom()
	bottomOffset := m.detail.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = u.(Model)
	if m.detail.YOffset >= bottomOffset {
		t.Fatal("Ctrl+U did not scroll PR preview up")
	}
	topOffset := m.detail.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = u.(Model)
	if m.detail.YOffset <= topOffset {
		t.Fatal("Ctrl+D did not scroll PR preview down")
	}
}

func TestConversationVimNavigation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.active = conversationTab
	for i := 0; i < 8; i++ {
		m.cache.Comments = append(m.cache.Comments, gh.Comment{NodeID: fmt.Sprintf("comment-%d", i), Body: fmt.Sprintf("comment %d", i), CreatedAt: "2026-08-08T00:00:00Z"})
	}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = u.(Model)
	if m.cursors[conversationTab] != 0 {
		t.Fatalf("conversation gg = %d", m.cursors[conversationTab])
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = u.(Model)
	if m.cursors[conversationTab] != m.activeLen()-1 {
		t.Fatalf("conversation G = %d, want %d", m.cursors[conversationTab], m.activeLen()-1)
	}
	m.list.Height = 3
	m.list.SetContent(strings.Repeat("conversation\n", 20))
	m.list.GotoBottom()
	bottomOffset := m.list.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = u.(Model)
	if m.list.YOffset >= bottomOffset {
		t.Fatal("conversation Ctrl+U did not scroll up")
	}
	topOffset := m.list.YOffset
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = u.(Model)
	if m.list.YOffset <= topOffset {
		t.Fatal("conversation Ctrl+D did not scroll down")
	}
}

func TestBackToListPicksTheOriginOrFirstMatchingView(t *testing.T) {
	newModel := func() Model {
		m := testModel()
		m.viewerLogin = "me"
		m.currentBranch = "feature"
		m.navigator = gh.NewNavigatorCache()
		m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")
		m.views = []config.View{
			{Name: "Assigned", Query: "assignee:@me"},
			{Name: "Authored", Query: "author:@me"},
			{Name: "All", Query: ""},
		}
		return m
	}
	authored := gh.PR{Number: 7, State: "OPEN", Author: gh.PRUser{Login: "me"}, HeadRefName: "feature"}

	// Entered from a tab: b returns to that tab even when others match.
	m := newModel()
	m.screen, m.prView = prListScreen, 2
	m.openPRs = []gh.PR{authored}
	m.prStacks = buildPRStacks(m.openPRs)
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if !m.detailOriginSet || m.detailOrigin != 2 {
		t.Fatalf("origin not recorded: set=%v view=%d", m.detailOriginSet, m.detailOrigin)
	}
	m.screen, m.cache.PR = detailScreen, &authored
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	back := u.(Model)
	if back.screen != prListScreen || back.prView != 2 {
		t.Fatalf("did not return to the origin tab: screen=%v view=%d", back.screen, back.prView)
	}
	if back.detailOriginSet {
		t.Fatal("origin outlived the return")
	}

	// Opened at startup: the first tab containing the PR wins (Assigned does
	// not match, Authored does), and the PR keeps the selection.
	startup := newModel()
	startup.screen, startup.prView = detailScreen, 0
	startup.cache.PR = &authored
	startup.navigator.PRs = []gh.PR{authored}
	u, _ = startup.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	landed := u.(Model)
	if landed.prView != 1 {
		t.Fatalf("landed on view %d (%q), want Authored", landed.prView, landed.viewName(landed.prView))
	}
	if landed.selectedPRNumber() != 7 {
		t.Fatalf("selection = #%d, want the PR just left", landed.selectedPRNumber())
	}

	// No tab contains it: fall back to the first.
	orphan := newModel()
	orphan.views = orphan.views[:2] // Assigned, Authored
	orphan.screen, orphan.prView = detailScreen, 1
	someoneElse := gh.PR{Number: 9, State: "OPEN", Author: gh.PRUser{Login: "you"}}
	orphan.cache.PR = &someoneElse
	u, _ = orphan.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if got := u.(Model).prView; got != 0 {
		t.Fatalf("fallback landed on view %d, want the first", got)
	}
}

func TestPRListFilterEditingAndViewKeys(t *testing.T) {
	m := testModel()
	m.screen, m.viewerLogin = prListScreen, "me"
	m.navigator.PRs = []gh.PR{{Number: 1, Title: "Bug", Labels: []gh.PRLabel{{Name: "bug"}}}, {Number: 2, Title: "Feature", ReviewRequests: []gh.PRUser{{Login: "me"}}}}
	m.applyPRFilters(0)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	m.prCursor = 1
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("label:bug")})
	m = u.(Model)
	if !m.filterEditing || m.filterQuery != "label:bug" || len(m.openPRs) != 2 {
		t.Fatalf("editing unexpectedly fetched/filtered: editing:%v query:%q prs:%#v", m.filterEditing, m.filterQuery, m.openPRs)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if m.filterQuery != "" || len(m.openPRs) != 2 || m.selectedPRNumber() != 2 {
		t.Fatalf("Esc did not clear filter/restore selection: %q %#v selected=%d", m.filterQuery, m.openPRs, m.selectedPRNumber())
	}
	if m.help.Width != 120 {
		t.Fatalf("help width = %d", m.help.Width)
	}
	m.prView = assignedView
	m.applyPRFilters(0)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	m = u.(Model)
	if m.prView != reviewRequestedView || len(m.openPRs) != 1 || m.openPRs[0].Number != 2 {
		t.Fatalf("next view = %v %#v", m.prView, m.openPRs)
	}
	plain := ansi.Strip(m.renderPRListHeader())
	for _, want := range []string{"[ Assigned ? ]", "[ Review requested 1 ]", "[ All 2 ]", "[ Authored ? ]", "[ Needs me 1 ]", "[ Closed ? ]"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("header missing %q: %q", want, plain)
		}
	}
}

func TestPRListEnterOpensRemoteWithoutChangingCheckout(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.currentBranch = "main"
	m.openPRs = []gh.PR{{Number: 14, Title: "remote", HeadRefName: "feature", BaseRefName: "main", HeadRefOID: "abc"}}
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd == nil || m.screen != detailScreen || !m.remote || m.head != "feature" || m.headRev != "refs/live-pr/pulls/14/head" || m.diffTerminal != nil {
		t.Fatalf("remote target not opened: screen=%v remote=%v head=%q rev=%q terminal=%v", m.screen, m.remote, m.head, m.headRev, m.diffTerminal)
	}
}

func TestDetailBReturnsToPRList(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	m.currentBranch, m.defaultBranch = "feature", "main"
	m.localAvailable, m.localTitle = true, "local work"
	m.openPRs = m.withLocalPR(nil)
	m.focusDiff = true
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = u.(Model)
	if m.screen != prListScreen || m.diffTerminal != nil || !strings.Contains(ansi.Strip(m.View()), "Local PR") {
		t.Fatalf("b did not return to local PR list: screen=%v terminal=%v view=%q", m.screen, m.diffTerminal, ansi.Strip(m.View()))
	}
}

func TestDetailBRestoresRemotePRSelection(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.cache.PR = &gh.PR{Number: 22, State: "OPEN", Title: "selected"}
	m.prView, m.prListState = allPRsView, openPRListState
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.prPages = map[string]prPageState{m.activePRPage: {prs: []gh.PR{{Number: 11}, {Number: 22}}, loaded: true, fresh: true}}
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = u.(Model)
	if m.screen != prListScreen || m.selectedPRNumber() != 22 {
		t.Fatalf("restored selection = screen:%v PR:%d", m.screen, m.selectedPRNumber())
	}
}

func TestRefreshAndPublishAreMutuallyExclusive(t *testing.T) {
	m := testModel()
	m.refreshing = true
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = u.(Model)
	if cmd != nil || m.publishing || !strings.Contains(m.status, "wait") {
		t.Fatal("publish should wait for refresh")
	}
	// r during a publish reports the busy state instead of starting a second
	// fetch — and instead of silently doing nothing.
	m.refreshing, m.publishing = false, true
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if got := u.(Model); got.refreshing || !got.publishing {
		t.Fatalf("refresh overlapped publish: refreshing=%v publishing=%v", got.refreshing, got.publishing)
	}
}

func TestPRViewNavigationSupportsHL(t *testing.T) {
	m := testModel()
	m.screen, m.prView = prListScreen, allPRsView
	m.activePRPage = prPageKey(allPRsView, openPRListState, "")
	m.prPages = map[string]prPageState{}
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = u.(Model)
	if m.prView != authoredView {
		t.Fatalf("l view = %v", m.prView)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	m = u.(Model)
	if m.prView != allPRsView {
		t.Fatalf("h view = %v", m.prView)
	}
}

func TestReviewFocusKeys(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	// Tab from conversation focuses the review.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("Tab should focus review")
	}
	footer := ansi.Strip(m.renderFooter())
	if !strings.Contains(footer, "Tab conversation") {
		t.Fatalf("focused footer is misleading: %q", footer)
	}
	// Shift+Tab expands the review to full width.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if !m.reviewWide || !m.focusDiff {
		t.Fatal("Shift+Tab should make the review full width")
	}
	// Shift+Tab again restores the split.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if m.reviewWide {
		t.Fatal("second Shift+Tab should restore the split")
	}
	// Tab from review returns to conversation.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = u.(Model)
	if m.focusDiff || m.reviewWide {
		t.Fatal("Tab from review should return to conversation")
	}
	// Shift+Tab from conversation expands conversation to full width.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if !m.reviewWide || m.focusDiff {
		t.Fatal("Shift+Tab from conversation should expand conversation full width")
	}
	// Shift+Tab again restores the split.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = u.(Model)
	if m.reviewWide {
		t.Fatal("Shift+Tab should restore the split")
	}
	// q quits from conversation.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Fatal("q on the conversation should quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("conversation q returned %T", cmd())
	}
}

func TestStaticReviewFocusKeys(t *testing.T) {
	m := testModel()
	m.diffCommand = "cat"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = u.(Model)
	if !m.focusDiff {
		t.Fatal("l should focus the static review fallback")
	}
	// q from the review should quit (not return to conversation — use Tab).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q from the focused review should quit")
	}
}

func TestPublishIsExplicitAndSingleFlight(t *testing.T) {
	m := testModel()
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = u.(Model)
	if cmd == nil || !m.publishing {
		t.Fatal("p should explicitly start publishing")
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")}); duplicate != nil {
		t.Fatal("a second publish must not start while one is in flight")
	}
}

// TestOverloadedKeysDisambiguateByScreen locks the SetEnabled toggling that
// keeps c/l from colliding: on the PR list c=Checkout and l=NextView; on the
// detail screen c=Commits and l=FocusRight. A missed SetEnabled would surface
// here as both bindings enabled on one screen.
func TestOverloadedKeysDisambiguateByScreen(t *testing.T) {
	m := testModel()
	m.ready = true
	m.w, m.h = 120, 40
	m.list = viewport.New(80, 30)
	m.detail = viewport.New(80, 30)

	m.screen = prListScreen
	m.sync()
	if m.keys.FocusRight.Enabled() {
		t.Error("PR list: l should be NextView, not FocusRight")
	}
	if m.keys.Commits.Enabled() {
		t.Error("PR list: c should be Checkout, not Commits")
	}
	if !m.keys.NextView.Enabled() {
		t.Error("PR list: NextView (l) should be enabled")
	}

	m.screen = detailScreen
	m.active = conversationTab
	m.sync()
	if m.keys.NextView.Enabled() {
		t.Error("detail: l should be FocusRight, not NextView")
	}
	if m.keys.Checkout.Enabled() {
		t.Error("detail: c should be Commits, not Checkout")
	}
	if !m.keys.FocusRight.Enabled() {
		t.Error("detail: FocusRight (l) should be enabled")
	}
}

func TestCopyURLKeyOnListAndDetail(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.openPRs = []gh.PR{{Number: 7, URL: "https://example/pr/7", Title: "seven"}}
	m.prStacks = buildPRStacks(m.openPRs)
	m.prCursor = 0

	// y copies the selected row's URL without opening a browser.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y produced no command on the PR list")
	}
	if m.selectedBrowseURL() != "https://example/pr/7" {
		t.Fatalf("selected URL = %q", m.selectedBrowseURL())
	}
	// The result reports through browserDone, which the footer already words.
	done, _ := m.Update(browserDone{copied: true})
	if got := done.(Model).notice; got != "URL copied to clipboard" {
		t.Fatalf("notice = %q", got)
	}

	// A row without a URL (the synthetic local PR) copies nothing.
	local := testModel()
	local.screen = prListScreen
	local.openPRs = []gh.PR{{Number: 0, Title: "local"}}
	local.prStacks = buildPRStacks(local.openPRs)
	if local.copySelectedURL() != nil {
		t.Fatal("local PR offered a URL to copy")
	}

	// Detail screen: y copies the focused conversation item's URL.
	detail := testModel()
	detail.screen, detail.active = detailScreen, conversationTab
	detail.cache.PR = &gh.PR{Number: 7, URL: "https://example/pr/7"}
	detail.conversationDirty = true
	if url := detail.selectedBrowseURL(); url == "" {
		t.Fatal("detail conversation exposed no URL")
	}
	if _, cmd := detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}); cmd == nil {
		t.Fatal("y produced no command on the detail screen")
	}
}

func TestPRListHelpKeyTogglesFullHelp(t *testing.T) {
	m := testModel()
	m.screen = prListScreen

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = u.(Model)
	if !m.help.ShowAll {
		t.Fatal("? on the PR list did not open the full help")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if u.(Model).help.ShowAll {
		t.Fatal("second ? on the PR list did not close the full help")
	}
}
