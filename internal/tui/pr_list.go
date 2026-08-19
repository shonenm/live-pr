package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	gh "github.com/shonenm/live-pr/internal/github"
	md "github.com/shonenm/live-pr/internal/markdown"
	"github.com/shonenm/live-pr/internal/prfilter"
	"github.com/shonenm/live-pr/internal/theme"
)

// prListModel groups the state that only the PR-list screen touches: the
// paged PR collections, tab/filter selection, stack grouping, the row render
// cache, and preview bookkeeping. Shared state (views config, navigator,
// viewports, spinners) stays on Model.
type prListModel struct {
	all                       []gh.PR
	filtered                  []gh.PR
	open                      []gh.PR
	previewLoading            map[int]bool
	previewLoaded             map[int]bool
	previewedPR               int
	view                      prView
	state                     prListState
	pages                     map[string]prPageState
	activePage                string
	viewCounts                []int
	viewCountKnown            []bool
	viewCountsValid           bool
	filterQuery               string
	filterBeforeEdit          string
	filterSelectionBeforeEdit int
	filterEditing             bool
	stacks                    []prfilter.Stack
	pageIndex                 map[int]prPageRef
	rowCache                  map[prRowCacheKey][]string
	collapsedStacks           map[string]bool
	cursor                    int
	refreshing                bool
	generation                uint64
}

// prPageRef locates one PR inside prListModel.pages by page key and position.
type prPageRef struct {
	key string
	idx int
}

// pagePR returns a pointer to the pages entry holding number, keeping a lazy
// number→position index so per-PR updates avoid rescanning every page. The
// cached ref is verified before use and the index rebuilt on any miss, so the
// many sites that replace or reset pages never have to invalidate it. The
// pointer aliases the page's backing array: writes through it are visible
// without reassigning pages[key].
func (l *prListModel) pagePR(number int) *gh.PR {
	if ref, ok := l.pageIndex[number]; ok {
		if page, exists := l.pages[ref.key]; exists && ref.idx < len(page.prs) && page.prs[ref.idx].Number == number {
			return &page.prs[ref.idx]
		}
	}
	l.pageIndex = map[int]prPageRef{}
	for key, page := range l.pages {
		for i := range page.prs {
			l.pageIndex[page.prs[i].Number] = prPageRef{key: key, idx: i}
		}
	}
	if ref, ok := l.pageIndex[number]; ok {
		return &l.pages[ref.key].prs[ref.idx]
	}
	return nil
}

func (s prListState) String() string {
	if s == closedPRListState {
		return "CLOSED"
	}
	return "OPEN"
}

func (s prListState) Label() string {
	if s == closedPRListState {
		return "Closed"
	}
	return "Open"
}

func filterPRListState(query string) (prListState, bool) {
	for _, token := range strings.Fields(strings.ToLower(query)) {
		key, value, ok := strings.Cut(token, ":")
		if !ok || (key != "is" && key != "state") {
			continue
		}
		switch value {
		case "closed":
			return closedPRListState, true
		case "open":
			return openPRListState, true
		}
	}
	return openPRListState, false
}

func (m Model) desiredPRListState() prListState {
	if state, ok := filterPRListState(m.prList.filterQuery); ok {
		return state
	}
	return m.standardPRListState(m.prList.view)
}

// viewDef returns the definition behind a tab index. Out-of-range indexes
// can survive a config edit that shortened the list, so they fall back to a
// harmless empty view rather than panicking.
func (m Model) viewDef(view prView) config.View {
	if view < 0 || int(view) >= len(m.views) {
		return config.View{}
	}
	return m.views[view]
}

func (m Model) standardPRListState(view prView) prListState {
	if m.viewDef(view).Closed() {
		return closedPRListState
	}
	return openPRListState
}

func (m *Model) seedPRPages() {
	if m.prList.pages == nil {
		m.prList.pages = map[string]prPageState{}
	}
	byNumber := make(map[int]gh.PR, len(m.navigator.PRs))
	for _, pr := range m.navigator.PRs {
		byNumber[pr.Number] = pr
	}
	for view := prView(0); int(view) < len(m.views); view++ {
		state := m.standardPRListState(view)
		key := prPageKey(view, state, "")
		if _, exists := m.prList.pages[key]; exists {
			continue
		}
		if cached, ok := m.navigator.Views[m.viewName(view)]; ok {
			prs := make([]gh.PR, 0, len(cached.Numbers))
			for _, number := range cached.Numbers {
				if pr, exists := byNumber[number]; exists {
					prs = append(prs, pr)
				}
			}
			m.prList.pages[key] = prPageState{prs: prs, total: cached.TotalCount, loaded: true}
			continue
		}
		var prs []gh.PR
		for _, pr := range m.navigator.PRs {
			if matchesListState(pr, state) && m.matchesView(pr, view) {
				prs = append(prs, pr)
			}
		}
		if len(prs) > 0 {
			m.prList.pages[key] = prPageState{prs: prs, total: len(prs), loaded: true}
			m.navigator.SetView(m.viewName(view), prs, len(prs), m.navigator.FetchedAt)
		}
	}
}

func prPageKey(view prView, state prListState, filter string) string {
	return fmt.Sprintf("%d:%d:%s", view, state, strings.Join(strings.Fields(strings.ToLower(filter)), " "))
}

// prViewSearch builds the GitHub search for a tab: the requested state, the
// view's own query (minus its state tokens, which state already carries), and
// the server-side part of the user's filter.
func (m Model) prViewSearch(view prView, state prListState, filter string) string {
	terms := []string{"is:" + strings.ToLower(state.String())}
	if query, _ := prfilter.Split(m.viewDef(view).Query); query != "" {
		terms = append(terms, query)
	}
	server, _ := prfilter.Split(filter)
	if server != "" {
		terms = append(terms, server)
	}
	return strings.Join(terms, " ")
}

func (m *Model) requestPRPage(reset bool) tea.Cmd {
	if m.prList.pages == nil {
		m.prList.pages = map[string]prPageState{}
	}
	key := m.prList.activePage
	page := m.prList.pages[key]
	if page.loading || (!reset && (!page.loaded || !page.hasNext)) {
		return nil
	}
	cursor, appendPage := page.endCursor, true
	if reset {
		cursor, appendPage = "", false
	}
	page.loading = true
	m.prList.pages[key] = page
	m.prList.refreshing = true
	m.githubStatus = "GitHub: fetching " + m.viewName(m.prList.view) + " pull requests…"
	return tea.Batch(fetchPRList(m.client, m.prList.generation, key, m.prViewSearch(m.prList.view, m.prList.state, m.prList.filterQuery), cursor, appendPage), m.startSpinner())
}

func (m *Model) applyPRViewState(selectedNumber int) tea.Cmd {
	m.seedPRPages()
	desired := m.desiredPRListState()
	key := prPageKey(m.prList.view, desired, m.prList.filterQuery)
	if key != m.prList.activePage {
		m.prList.generation++
		m.prList.activePage = key
		m.prList.previewLoading = map[int]bool{}
		m.prList.previewLoaded = map[int]bool{}
	}
	m.prList.state = desired
	m.applyPRFilters(selectedNumber)
	if page := m.prList.pages[key]; page.loading {
		m.prList.refreshing = true
		m.githubStatus = "GitHub: fetching " + m.viewName(m.prList.view) + " pull requests…"
		return m.sync()
	} else if page.fresh {
		m.prList.refreshing = false
		m.githubStatus = "GitHub: cached " + m.viewName(m.prList.view) + " page"
		return m.sync()
	}
	return tea.Batch(m.sync(), m.requestPRPage(true))
}

// viewName is the tab label, and the key its page cache is stored under.
func (m Model) viewName(view prView) string {
	if name := m.viewDef(view).Name; name != "" {
		return name
	}
	return "All"
}

func matchesListState(pr gh.PR, state prListState) bool {
	if pr.Number == 0 || pr.State == "" {
		// Local and older sparse cache entries belong to the open navigator.
		return state == openPRListState
	}
	if state == closedPRListState {
		return strings.EqualFold(pr.State, "CLOSED") || strings.EqualFold(pr.State, "MERGED")
	}
	return strings.EqualFold(pr.State, state.String())
}

func upsertPR(prs []gh.PR, updated gh.PR) []gh.PR {
	result := append([]gh.PR(nil), prs...)
	for i := range result {
		if result[i].Number == updated.Number {
			result[i] = updated
			return result
		}
	}
	return append([]gh.PR{updated}, result...)
}

// matchesView evaluates a tab's own query locally, for counts and for the
// cached rows shown before a page arrives. State tokens are dropped: the
// caller pairs this with matchesListState.
func (m Model) matchesView(pr gh.PR, view prView) bool {
	if pr.Number == 0 {
		// The synthetic local PR belongs to every open tab.
		return !m.viewDef(view).Closed()
	}
	// Evaluate the whole query, not just the server half: the parts GitHub
	// cannot express (OR groups, ci:, merge:) are exactly the ones that have
	// to be decided here.
	query := strings.TrimSpace(m.viewDef(view).Query)
	if query == "" {
		return true
	}
	return prfilter.Matches(pr, query, m.viewerLogin)
}

// maxPRRowCacheEntries bounds the render cache; rows key on their full render
// inputs, so stale entries are merely unused, never wrong.
const maxPRRowCacheEntries = 512

func (m *Model) applyPRFilters(selectedNumber int) {
	if m.prList.rowCache == nil {
		m.prList.rowCache = map[prRowCacheKey][]string{}
	} else if len(m.prList.rowCache) > maxPRRowCacheEntries {
		// ponytail: full reset over LRU — refilling a screenful is cheap.
		clear(m.prList.rowCache)
	}
	if m.prList.collapsedStacks == nil {
		m.prList.collapsedStacks = map[string]bool{}
	}
	page, paged := m.prList.pages[m.prList.activePage]
	var source []gh.PR
	if page.loaded {
		source = append([]gh.PR(nil), page.prs...)
	} else {
		// Legacy/cache fallback before the first page arrives.
		source = append([]gh.PR(nil), m.navigator.PRs...)
	}
	slices.SortFunc(source, func(a, b gh.PR) int { return a.Number - b.Number })
	sourceNumbers := make(map[int]bool, len(source))
	for _, pr := range source {
		sourceNumbers[pr.Number] = true
	}
	m.prList.all = m.withLocalPR(source)
	m.recomputeViewCounts(page, paged)
	m.prList.filtered = make([]gh.PR, 0, len(m.prList.all))
	_, localFilter := prfilter.Split(m.prList.filterQuery)
	// Server rows are trusted for the qualifiers GitHub ran; whatever it
	// could not evaluate (OR groups, ci:, merge:) is still ours to apply.
	_, viewLocal := prfilter.Split(m.viewDef(m.prList.view).Query)
	for _, pr := range m.prList.all {
		if page.loaded && pr.Number > 0 && sourceNumbers[pr.Number] {
			if viewLocal != "" && !prfilter.Matches(pr, viewLocal, m.viewerLogin) {
				continue
			}
			if localFilter == "" || prfilter.Matches(pr, localFilter, m.viewerLogin) {
				m.prList.filtered = append(m.prList.filtered, pr)
			}
			continue
		}
		if matchesListState(pr, m.prList.state) && m.matchesView(pr, m.prList.view) && prfilter.Matches(pr, m.prList.filterQuery, m.viewerLogin) {
			m.prList.filtered = append(m.prList.filtered, pr)
		}
	}
	slices.SortFunc(m.prList.filtered, func(a, b gh.PR) int { return a.Number - b.Number })
	if m.prList.state == closedPRListState {
		m.prList.stacks = prfilter.SingleStacks(m.prList.filtered)
	} else {
		m.prList.stacks = prfilter.BuildStacks(m.prList.filtered)
	}
	m.prList.open = make([]gh.PR, 0, len(m.prList.filtered))
	for _, stack := range m.prList.stacks {
		entries := stack.Entries
		if len(entries) > 1 && m.prList.collapsedStacks[stack.ID] {
			entries = entries[:1]
		}
		for _, entry := range entries {
			m.prList.open = append(m.prList.open, entry.PR)
		}
	}
	m.prList.restorePRSelection(selectedNumber)
}

// recomputeViewCounts refreshes the per-view PR counts shown in the tab bar,
// preferring a loaded page's total, then the navigator, then the local set.
func (m *Model) recomputeViewCounts(page prPageState, paged bool) {
	m.prList.viewCounts = make([]int, len(m.views))
	m.prList.viewCountKnown = make([]bool, len(m.views))
	for view := prView(0); int(view) < len(m.views); view++ {
		state := m.standardPRListState(view)
		_, localOnly := prfilter.Split(m.viewDef(view).Query)
		cached, ok := m.prList.pages[prPageKey(view, state, "")]
		switch {
		case ok && cached.loaded && localOnly == "":
			m.prList.viewCounts[view], m.prList.viewCountKnown[view] = cached.total, true
		case ok && cached.loaded:
			// The server could not evaluate part of this view, so its total
			// counts rows the tab filters out. Count what was loaded.
			for _, pr := range cached.prs {
				if matchesListState(pr, state) && m.matchesView(pr, view) {
					m.prList.viewCounts[view]++
				}
			}
			m.prList.viewCountKnown[view] = true
		case page.loaded:
			for _, pr := range m.navigator.PRs {
				if matchesListState(pr, state) && m.matchesView(pr, view) {
					m.prList.viewCounts[view]++
				}
			}
		}
	}
	if !paged || !page.loaded {
		for view := prView(0); int(view) < len(m.views); view++ {
			// A cached page already produced this view's exact total; adding
			// the prList.all fallback on top would double-count it.
			if m.prList.viewCountKnown[view] {
				continue
			}
			state := m.standardPRListState(view)
			for _, pr := range m.prList.all {
				if matchesListState(pr, state) && m.matchesView(pr, view) {
					m.prList.viewCounts[view]++
				}
			}
			m.prList.viewCountKnown[view] = true
		}
	}
	if m.localAvailable && page.loaded {
		local := gh.PR{State: "LOCAL", Author: gh.PRUser{Login: m.viewerLogin}}
		for view := prView(0); int(view) < len(m.views); view++ {
			if m.matchesView(local, view) {
				m.prList.viewCounts[view]++
			}
		}
	}
	m.prList.viewCountsValid = true
}

func (l prListModel) stackForPR(number int) (prfilter.Stack, bool) {
	for _, stack := range l.stacks {
		if len(stack.Entries) < 2 {
			continue
		}
		for _, entry := range stack.Entries {
			if entry.PR.Number == number {
				return stack, true
			}
		}
	}
	return prfilter.Stack{}, false
}

func (m Model) withLocalPR(prs []gh.PR) []gh.PR {
	items := append([]gh.PR(nil), prs...)
	if m.cache.PR != nil && m.isCurrentTargetPR(*m.cache.PR) {
		for _, pr := range items {
			if pr.Number == m.cache.PR.Number {
				return items
			}
		}
		return append([]gh.PR{*m.cache.PR}, items...)
	}
	if !m.localAvailable {
		return items
	}
	for _, pr := range items {
		if m.isCurrentTargetPR(pr) {
			return items
		}
	}
	title := m.localTitle
	if title == "" {
		title = m.currentBranch
	}
	local := gh.PR{Title: title, State: "LOCAL", BaseRefName: m.defaultBranch, HeadRefName: m.currentBranch, ChangedFiles: m.localStats.Files, Additions: m.localStats.Additions, Deletions: m.localStats.Deletions, CommitCount: m.localCommitCount}
	return append([]gh.PR{local}, items...)
}

func (l prListModel) selectedPR() *gh.PR {
	if l.cursor < 0 || l.cursor >= len(l.open) {
		return nil
	}
	return &l.open[l.cursor]
}

func (l prListModel) selectedPRNumber() int {
	if pr := l.selectedPR(); pr != nil {
		return pr.Number
	}
	return 0
}

func (l *prListModel) restorePRSelection(number int) {
	if number != 0 {
		for i := range l.open {
			if l.open[i].Number == number {
				l.cursor = i
				return
			}
		}
	}
	if len(l.open) == 0 {
		l.cursor = 0
	} else if l.cursor >= len(l.open) {
		l.cursor = len(l.open) - 1
	}
}

func (m *Model) ensureSelectedPRPreview() tea.Cmd {
	pr := m.prList.selectedPR()
	if pr == nil || pr.Number <= 0 || pr.PreviewLoaded {
		return nil
	}
	if m.prList.previewLoading == nil {
		m.prList.previewLoading = map[int]bool{}
	}
	if m.prList.previewLoaded == nil {
		m.prList.previewLoaded = map[int]bool{}
	}
	if m.prList.previewLoading[pr.Number] || m.prList.previewLoaded[pr.Number] {
		return nil
	}
	m.prList.previewLoading[pr.Number] = true
	m.status = fmt.Sprintf("loading PR #%d preview…", pr.Number)
	return fetchPRPreview(m.client, pr.Number, m.prList.generation)
}

func (m Model) renderPRListHeader() string {
	tabs := make([]string, 0, len(m.views))
	activeBg := lipgloss.Color(cSelectedBg)
	for view := prView(0); int(view) < len(m.views); view++ {
		name, count := m.viewName(view), "?"
		if !m.prList.viewCountsValid || (int(view) < len(m.prList.viewCountKnown) && m.prList.viewCountKnown[view]) {
			count = strconv.Itoa(m.viewCount(view))
		}
		border, content := stMuted, stMuted
		if view == m.prList.view {
			border = stAccent
			content = lipgloss.NewStyle().Background(activeBg).Foreground(lipgloss.Color(cAccent)).Bold(true)
		}
		tabs = append(tabs, border.Render("[")+content.Render(fmt.Sprintf(" %s %s ", name, count))+border.Render("]"))
	}
	available := m.w
	if m.w >= logoWidth+40 {
		available -= logoWidth
	}
	heading := "Pull requests"
	if m.repository != "" {
		heading = m.repository + " · " + heading
	}
	tabRows := []string{stBold.Render(heading)}
	if available > 0 && available < 60 {
		tabRows[0] = stBold.Render(m.repository)
	}
	for _, tab := range tabs {
		separator := " "
		if tabRows[len(tabRows)-1] == "" {
			separator = ""
		}
		candidate := tabRows[len(tabRows)-1] + separator + tab
		if available > 0 && lipgloss.Width(candidate) > available && tabRows[len(tabRows)-1] != "" {
			tabRows = append(tabRows, tab)
		} else {
			tabRows[len(tabRows)-1] = candidate
		}
	}
	filter := stMuted.Render("/ filter (is:closed) · [/] views · space stacks")
	if m.prList.filterEditing {
		filter = stAccent.Render(" ") + stFg.Render(m.prList.filterQuery+"▌") + stMuted.Render(" · Enter search · Esc cancel")
	} else if m.prList.filterQuery != "" {
		filter = stAccent.Render(" ") + stFg.Render(m.prList.filterQuery) + stMuted.Render(" · Esc clear")
	}
	metrics := []string{}
	if page, ok := m.prList.pages[m.prList.activePage]; ok && page.loaded {
		metrics = append(metrics, fmt.Sprintf("%d/%d loaded", len(page.prs), page.total))
	}
	metrics = append(metrics, fmt.Sprintf("%d listed", len(m.prList.filtered)), "⎇ "+m.currentBranch)
	line2 := filter + stMuted.Render("   · "+strings.Join(metrics, " · "))
	return m.withLogo(lipgloss.JoinVertical(lipgloss.Left, append(tabRows, line2)...))
}

func (m Model) viewCount(view prView) int {
	if m.prList.viewCountsValid && view >= 0 && int(view) < len(m.prList.viewCounts) {
		return m.prList.viewCounts[view]
	}
	state := m.standardPRListState(view)
	count := 0
	for _, pr := range m.prList.all {
		if matchesListState(pr, state) && m.matchesView(pr, view) {
			count++
		}
	}
	return count
}

func (m Model) buildPRPreview() string {
	pr := m.prList.selectedPR()
	if pr == nil {
		return stMuted.Render("Select a pull request to preview it.")
	}
	width := max(20, m.detail.Width-2)
	identifier := "Local PR"
	if pr.Number > 0 {
		identifier = fmt.Sprintf("#%d", pr.Number)
	}
	var statusParts []string
	if pr.Number == 0 {
		statusParts = append(statusParts, stMuted.Render("● local"))
	} else if pr.State != "" {
		state := strings.ToLower(pr.State)
		glyph, style := stateGlyph(state)
		statusParts = append(statusParts, style.Render(glyph+" "+state))
	}
	if mergeText, mergeStyle := mergeState(*pr); mergeText != "" {
		statusParts = append(statusParts, mergeStyle.Render(mergeText))
	}
	statusParts = append(statusParts, prCheckSummary(*pr))
	statusLine := "  " + strings.Join(statusParts, "   ")
	lines := []string{
		stMuted.Render(identifier) + "  " + stBold.Render(pr.Title),
		stMuted.Render("⎇ ") + m.baseBranchStyle(pr.BaseRefName).Render(pr.BaseRefName) + stMuted.Render(" ← "+pr.HeadRefName),
		"",
		stBold.Render("Status"),
		statusLine,
	}
	// Comments belong with the conversation state, not the diff size stats.
	conversation := []string{}
	if pr.ReviewDecision != "" {
		conversation = append(conversation, reviewSummary(pr.ReviewDecision))
	}
	conversation = append(conversation, stFg.Render(fmt.Sprintf("%d comments", pr.CommentCount)))
	lines = append(lines, "  "+strings.Join(conversation, "   "))
	if !pr.PreviewLoaded && pr.Number > 0 {
		lines = append(lines, "  "+stMuted.Render("loading preview details…"))
	}
	lines = append(lines,
		"",
		stBold.Render("Size"),
		stFg.Render(fmt.Sprintf("  %d files   ", pr.ChangedFiles))+stGreenF.Render(fmt.Sprintf("+%d", pr.Additions))+stFg.Render("   ")+stRedF.Render(fmt.Sprintf("-%d", pr.Deletions))+stFg.Render(fmt.Sprintf("   %d commits", pr.CommitCount)),
		"",
		stBold.Render("Metadata"),
		"  "+m.previewPeople(*pr),
	)
	if len(pr.Labels) > 0 {
		pills := make([]string, 0, len(pr.Labels))
		for _, label := range pr.Labels {
			pills = append(pills, labelPill(label))
		}
		lines = append(lines, "  "+strings.Join(pills, " "))
	}
	if pr.UpdatedAt != "" {
		lines = append(lines, "  "+stMuted.Render("updated "+relativeTS(time.Now(), pr.UpdatedAt)))
	}
	lines = append(lines, "")
	if pr.Number == 0 {
		lines = append(lines, "  "+stMuted.Render("(local PR has no GitHub conversation yet)"))
	} else {
		body := pr.Body
		if strings.TrimSpace(body) == "" {
			body = "(no description provided)"
		}
		header := m.userIcon(pr.Author.Login) + stMuted.Render(" @"+pr.Author.Login+" · description · "+relativeTS(time.Now(), pr.CreatedAt))
		lines = append(lines, cardLines(header, previewMarkdown(body, width-7, 10), false, width, cCloudBorder)...)
		if len(pr.Conversation) > 0 {
			comment := pr.Conversation[0]
			header = m.userIcon(comment.Author.Login) + stMuted.Render(" @"+comment.Author.Login+" · comment · "+relativeTS(time.Now(), comment.CreatedAt))
			lines = append(lines, "")
			lines = append(lines, cardLines(header, previewMarkdown(comment.Body, width-7, 5), false, width, cCloudBorder)...)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) previewPeople(pr gh.PR) string {
	parts := []string{}
	if pr.Author.Login != "" {
		parts = append(parts, stMuted.Render("author ")+m.userLabel(pr.Author))
	}
	if len(pr.Assignees) > 0 {
		users := make([]string, 0, len(pr.Assignees))
		for _, user := range pr.Assignees {
			users = append(users, m.userLabel(user))
		}
		parts = append(parts, stMuted.Render("assigned ")+strings.Join(users, " "))
	}
	if len(parts) == 0 {
		return stMuted.Render("unassigned")
	}
	return strings.Join(parts, stMuted.Render(" · "))
}

func reviewSummary(decision string) string {
	if strings.TrimSpace(decision) == "" {
		return stMuted.Render("review pending")
	}
	label := "review " + strings.ToLower(strings.ReplaceAll(decision, "_", " "))
	switch strings.ToUpper(decision) {
	case "APPROVED":
		return stGreenF.Render(label)
	case "CHANGES_REQUESTED":
		return stRedF.Render(label)
	case "REVIEW_REQUIRED":
		return stAttention.Render(label)
	default:
		return stMuted.Render(label)
	}
}

// reviewBadge is the compact list-row form of reviewSummary. Unknown or
// missing decisions (older cached rows) render nothing.
func reviewBadge(decision string) (string, lipgloss.Style) {
	switch strings.ToUpper(strings.TrimSpace(decision)) {
	case "APPROVED":
		return "✓ approved", stGreenF
	case "CHANGES_REQUESTED":
		return "± changes", stRedF
	case "REVIEW_REQUIRED":
		return "◌ review", stAttention
	default:
		return "", stMuted
	}
}

func mergeState(pr gh.PR) (string, lipgloss.Style) {
	if pr.Number == 0 {
		return "local", stMuted
	}
	if strings.EqualFold(pr.State, "MERGED") || strings.EqualFold(pr.State, "CLOSED") {
		return "", stMuted // merge readiness is meaningless once the PR is done
	}
	state := strings.ToLower(strings.ReplaceAll(pr.MergeStateStatus, "_", " "))
	if pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return "⚠ conflicts", stRedF
	}
	if state == "" {
		state = strings.ToLower(pr.Mergeable)
	}
	if state == "" {
		state = "merge unknown"
	}
	if pr.Mergeable == "MERGEABLE" && (pr.MergeStateStatus == "CLEAN" || pr.MergeStateStatus == "UNSTABLE") {
		return "⇄ mergeable", stGreenF
	}
	switch pr.MergeStateStatus {
	case "BLOCKED":
		return state, stRedF
	case "BEHIND", "HAS_HOOKS":
		return state, stAttention
	default:
		return state, stMuted
	}
}

func checkRollupState(checks []gh.PRCheck) string {
	health, _ := prfilter.CheckHealth(checks)
	switch health {
	case "passed":
		return "SUCCESS"
	case "failed":
		return "FAILURE"
	case "pending":
		return "PENDING"
	default:
		return ""
	}
}

func prCheckSummary(pr gh.PR) string {
	text, style := prCheckState(pr)
	return style.Render(text)
}

func prCheckCounts(pr gh.PR) string {
	if !pr.PreviewLoaded && len(pr.Checks) == 0 {
		return stMuted.Render("CI")
	}
	pending, failed, passed := prfilter.CheckCounts(pr.Checks)
	counts := make([]string, 0, 3)
	if pending > 0 {
		counts = append(counts, stAttention.Render(strconv.Itoa(pending)))
	}
	if failed > 0 {
		counts = append(counts, stRedF.Render(strconv.Itoa(failed)))
	}
	if passed > 0 {
		counts = append(counts, stGreenF.Render(strconv.Itoa(passed)))
	}
	if len(counts) == 0 {
		return stMuted.Render("CI")
	}
	return stMuted.Render("CI ") + strings.Join(counts, stMuted.Render("/"))
}

func prCheckState(pr gh.PR) (string, lipgloss.Style) {
	if !pr.PreviewLoaded && len(pr.Checks) == 0 {
		switch prfilter.CIHealth(pr) {
		case "passed":
			return "✓ CI passed", stGreenF
		case "failed":
			return "✗ CI failed", stRedF
		case "pending":
			return "◐ CI pending", stAttention
		default:
			return "CI loading", stMuted
		}
	}
	return checkState(pr.Checks)
}

func checkState(checks []gh.PRCheck) (string, lipgloss.Style) {
	health, count := prfilter.CheckHealth(checks)
	switch health {
	case "failed":
		return fmt.Sprintf("✗ CI %d failed", count), stRedF
	case "pending":
		return fmt.Sprintf("◐ CI %d pending", count), stAttention
	case "passed":
		return fmt.Sprintf("✓ CI %d passed", count), stGreenF
	default:
		return "CI no checks", stMuted
	}
}

func previewMarkdown(text string, width, maxLines int) string {
	// Pre-truncate before the glamour render: its cost scales with the whole
	// body, and everything past a few times the visible lines is cut anyway.
	if raw := strings.Split(text, "\n"); len(raw) > maxLines*3 {
		text = strings.Join(raw[:maxLines*3], "\n")
	}
	lines := strings.Split(md.Render(text, width), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], stMuted.Render("…"))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) buildPRListRows() (string, int) {
	if len(m.prList.open) == 0 {
		message := stMuted.Render("(no pull requests in this view)")
		if m.prList.refreshing {
			message = stMuted.Render("fetching " + strings.ToLower(m.prList.state.Label()) + " pull requests…")
		}
		return lipgloss.Place(max(1, m.list.Width), max(1, m.list.Height), lipgloss.Center, lipgloss.Center, message), 0
	}
	// open is derived from stacks in applyPRFilters, so a non-empty
	// list always has stacks.
	stacks := m.prList.stacks
	// Layout pass: place group headers (one line) and PR rows (three lines)
	// without rendering them, to learn the selected line and total height.
	type placedRow struct {
		line, stack, entry int // entry == -1 marks a group header
		selected           bool
	}
	placed := make([]placedRow, 0, len(m.prList.open)+len(stacks))
	line, openIndex, selectedLine := 0, 0, 0
	for si := range stacks {
		entries := stacks[si].Entries
		if len(entries) > 1 {
			placed = append(placed, placedRow{line: line, stack: si, entry: -1})
			line++
			if m.prList.collapsedStacks[stacks[si].ID] {
				entries = entries[:1]
			}
		}
		for ei := range entries {
			selected := openIndex == m.prList.cursor
			if selected {
				selectedLine = line
			}
			placed = append(placed, placedRow{line: line, stack: si, entry: ei, selected: selected})
			line += 3
			openIndex++
		}
	}
	// Render pass: only the rows within one viewport height of the visible
	// region and of the selected row become text; the rest stay empty lines.
	// The line count — and so the scroll geometry — matches a full render,
	// and syncPRListScreen fixes the offset right after SetContent, where it
	// either stays put, is clamped to the last page by SetContent, or is
	// pulled to the selected line by keepLineVisible: always inside the
	// window rendered here.
	lo, hi := 0, line
	if m.list.Height > 0 { // an unsized viewport renders everything
		lo = min(m.list.YOffset, selectedLine) - m.list.Height
		hi = max(m.list.YOffset+m.list.Height-1, selectedLine) + m.list.Height + 1
	}
	lines := make([]string, line)
	for _, row := range placed {
		stack := stacks[row.stack]
		if row.entry < 0 {
			if row.line < lo || row.line >= hi {
				continue
			}
			arrow := "▾"
			if m.prList.collapsedStacks[stack.ID] {
				arrow = "▸"
			}
			header := stMuted.Render(arrow+" ") + stBold.Render(stack.Title) + stMuted.Render(fmt.Sprintf(" · %d PRs", len(stack.Entries)))
			lines[row.line] = ansi.Truncate(header, max(10, m.list.Width), "…")
			continue
		}
		if row.line+3 <= lo || row.line >= hi {
			continue
		}
		entries := stack.Entries
		grouped := len(entries) > 1
		if grouped && m.prList.collapsedStacks[stack.ID] {
			entries = entries[:1]
		}
		entry := entries[row.entry]
		prefix := ""
		if grouped {
			marker := "├ "
			if row.entry == len(entries)-1 {
				marker = "└ "
			}
			prefix = strings.Repeat("  ", entry.Depth) + marker
		}
		var rendered []string
		if row.selected {
			rendered = m.renderPRRow(entry.PR, true, prefix)
		} else {
			rendered = m.cachedPRRow(entry.PR, prefix)
		}
		copy(lines[row.line:], rendered)
	}
	return strings.Join(lines, "\n"), selectedLine
}

func (m *Model) cachedPRRow(pr gh.PR, prefix string) []string {
	if m.prList.rowCache == nil {
		m.prList.rowCache = map[prRowCacheKey][]string{}
	}
	health, count := prfilter.CheckHealth(pr.Checks)
	key := prRowCacheKey{
		number: pr.Number, width: max(10, m.list.Width), additions: pr.Additions, deletions: pr.Deletions, checkCount: count,
		prefix: prefix, state: pr.State, title: pr.Title, author: pr.Author.Login, base: pr.BaseRefName, head: pr.HeadRefName,
		mergeable: pr.Mergeable, mergeState: pr.MergeStateStatus, checkHealth: health, rollup: pr.CheckRollupState,
		review: pr.ReviewDecision, draft: pr.IsDraft, previewLoaded: pr.PreviewLoaded, current: m.isCurrentTargetPR(pr),
	}
	if rows, ok := m.prList.rowCache[key]; ok {
		return rows
	}
	rows := m.renderPRRow(pr, false, prefix)
	m.prList.rowCache[key] = rows
	return rows
}

// rowSegment is one styled span of a PR row; login segments render the user
// icon, which weaves the selection background into its own colors.
type rowSegment struct {
	text  string
	style lipgloss.Style
	login string
}

func (m Model) renderSegments(segments []rowSegment, background string) string {
	var b strings.Builder
	for _, segment := range segments {
		if segment.login != "" {
			b.WriteString(m.userIconOn(segment.login, background))
			continue
		}
		style := segment.style
		if background != "" {
			style = style.Background(lipgloss.Color(background))
		}
		b.WriteString(style.Render(segment.text))
	}
	return b.String()
}

// prRowSegments assembles the two content lines of a PR row once; selection
// only changes the background and padding, never the content. current marks
// the PR whose branch is checked out locally.
func (m Model) prRowSegments(pr gh.PR, prefix string, current bool) (line, meta []rowSegment) {
	state := strings.ToLower(pr.State)
	if state == "" {
		state = "open"
	}
	identifier := fmt.Sprintf("#%d", pr.Number)
	if pr.IsDraft && state == "open" {
		state = "draft"
	}
	if pr.Number == 0 {
		state, identifier = "local", "Local PR"
	}
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	glyph, glyphStyle := stateGlyph(state)
	line = []rowSegment{
		{text: glyph, style: glyphStyle},
		{text: " " + prefix + identifier + " ", style: stMuted},
		{text: pr.Title, style: stBold},
	}
	meta = []rowSegment{
		{text: "   " + indent, style: stMuted},
		{text: state, style: glyphStyle},
	}
	if current && pr.Number > 0 {
		meta = append(meta, rowSegment{text: " · ", style: stMuted}, rowSegment{text: "⎇ checked out", style: stAttention})
	}
	if pr.Number > 0 {
		if mergeText, mergeStyle := mergeState(pr); mergeText != "" {
			meta = append(meta, rowSegment{text: " · ", style: stMuted}, rowSegment{text: mergeText, style: mergeStyle})
		}
		checkText, checkStyle := prCheckState(pr)
		meta = append(meta, rowSegment{text: " · ", style: stMuted}, rowSegment{text: checkText, style: checkStyle})
		if reviewText, reviewStyle := reviewBadge(pr.ReviewDecision); reviewText != "" {
			meta = append(meta, rowSegment{text: " · ", style: stMuted}, rowSegment{text: reviewText, style: reviewStyle})
		}
	}
	if pr.Additions != 0 || pr.Deletions != 0 {
		meta = append(meta,
			rowSegment{text: " · ", style: stMuted},
			rowSegment{text: fmt.Sprintf("+%d", pr.Additions), style: stGreenF},
			rowSegment{text: " ", style: stMuted},
			rowSegment{text: fmt.Sprintf("-%d", pr.Deletions), style: stRedF})
	}
	meta = append(meta, rowSegment{text: fmt.Sprintf(" · %s ← %s", pr.BaseRefName, pr.HeadRefName), style: stMuted})
	if pr.Number > 0 && pr.Author.Login != "" {
		meta = append(meta,
			rowSegment{text: " · ", style: stMuted},
			rowSegment{login: pr.Author.Login},
			rowSegment{text: " @" + pr.Author.Login, style: stMuted})
	}
	// Label pills close the meta line; cap at three so heavily-labelled PRs
	// don't dominate the row, with the overflow collapsed into "+N".
	const maxRowLabelPills = 3
	if pr.Number > 0 && len(pr.Labels) > 0 {
		meta = append(meta, rowSegment{text: " · ", style: stMuted})
		shown := min(len(pr.Labels), maxRowLabelPills)
		for i, label := range pr.Labels[:shown] {
			if i > 0 {
				meta = append(meta, rowSegment{text: " ", style: stMuted})
			}
			meta = append(meta, rowSegment{text: labelPill(label)})
		}
		if rest := len(pr.Labels) - shown; rest > 0 {
			meta = append(meta, rowSegment{text: fmt.Sprintf(" +%d", rest), style: stMuted})
		}
	}
	return line, meta
}

func (m Model) renderPRRow(pr gh.PR, selected bool, prefix string) []string {
	width := max(10, m.list.Width)
	current := m.isCurrentTargetPR(pr)
	lineSegments, metaSegments := m.prRowSegments(pr, prefix, current)
	if !selected {
		currentBar := " "
		if current {
			currentBar = stAttention.Render("▌")
		}
		line := "  " + currentBar + " " + m.renderSegments(lineSegments, "")
		meta := " " + m.renderSegments(metaSegments, "")
		return []string{ansi.Truncate(line, width, "…"), ansi.Truncate(meta, width, "…"), ""}
	}
	// Selected rows collapse to one highlight style, lazygit-style; the state
	// glyph keeps its color on the selection background, which paints the
	// whole row instead of an accent bar.
	bg := lipgloss.Color(cSelectedBg)
	rowSt := lipgloss.NewStyle().Foreground(lipgloss.Color(cFg)).Background(bg)
	mutedSt := lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted)).Background(bg)
	bar := rowSt.Render(" ")
	checkoutBar := rowSt.Render(" ")
	if current {
		checkoutBar = lipgloss.NewStyle().Foreground(lipgloss.Color(cAttention)).Background(bg).Render("▌")
	}
	line := bar + checkoutBar + rowSt.Render(" ") + m.renderSegments(lineSegments, cSelectedBg)
	meta := bar + m.renderSegments(metaSegments, cSelectedBg)
	return []string{padRow(line, width, rowSt), padRow(meta, width, mutedSt), ""}
}
func (m Model) renderPRMeta(pr gh.PR) string {
	assignees := stMuted.Render("unassigned")
	if len(pr.Assignees) > 0 {
		users := make([]string, 0, len(pr.Assignees))
		for _, user := range pr.Assignees {
			users = append(users, m.userLabel(user))
		}
		assignees = stMuted.Render("assigned ") + strings.Join(users, " ")
	}
	reviewers := ""
	if len(pr.ReviewRequests) > 0 {
		users := make([]string, 0, len(pr.ReviewRequests))
		for _, user := range pr.ReviewRequests {
			users = append(users, m.userLabel(user))
		}
		reviewers = "   " + stMuted.Render("review requested ") + strings.Join(users, " ")
	}
	labels := stMuted.Render("🏷 no labels")
	if len(pr.Labels) > 0 {
		pills := make([]string, 0, len(pr.Labels))
		for _, label := range pr.Labels {
			pills = append(pills, labelPill(label))
		}
		labels = stMuted.Render("🏷 ") + strings.Join(pills, " ")
	}
	line := "  " + prCheckCounts(pr) + "   " + reviewSummary(pr.ReviewDecision) + "   " + assignees + reviewers + "   " + labels
	if m.w > 0 {
		return ansi.Truncate(line, m.w, "…")
	}
	return line
}

func prStateBadgeColor(state string) string {
	switch strings.ToUpper(state) {
	case "OPEN":
		return cOpen
	case "MERGED":
		return cDoneEmphasis
	case "CLOSED":
		return cDangerEmphasis
	default:
		return cClosed
	}
}

func labelPill(label gh.PRLabel) string {
	background, foreground := cBorder, cFg
	color := strings.TrimPrefix(label.Color, "#")
	if rgb, err := strconv.ParseUint(color, 16, 24); err == nil && len(color) == 6 {
		background = "#" + color
		foreground = theme.ContrastingLabelForeground(rgb)
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(background)).Foreground(lipgloss.Color(foreground)).Padding(0, 1).Render(label.Name)
}

// stepView cycles the selected tab, tolerating an index left out of range by
// a shortened view list.
func (m Model) stepView(delta int) prView {
	count := len(m.views)
	if count == 0 {
		return 0
	}
	next := (int(m.prList.view)%count + count + delta%count) % count
	return prView(next)
}

// closedView finds a tab that lists closed pull requests, so the list can
// follow a branch whose PR just closed. A config without such a tab simply
// stays put.
func (m Model) closedView() (prView, bool) {
	for i, view := range m.views {
		if view.Closed() {
			return prView(i), true
		}
	}
	return 0, false
}
