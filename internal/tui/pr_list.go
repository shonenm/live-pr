package tui

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	gh "github.com/shonenm/live-pr/internal/github"
	md "github.com/shonenm/live-pr/internal/markdown"
)

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
	if state, ok := filterPRListState(m.filterQuery); ok {
		return state
	}
	return m.standardPRListState(m.prView)
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
	if m.prPages == nil {
		m.prPages = map[string]prPageState{}
	}
	byNumber := make(map[int]gh.PR, len(m.navigator.PRs))
	for _, pr := range m.navigator.PRs {
		byNumber[pr.Number] = pr
	}
	for view := prView(0); int(view) < len(m.views); view++ {
		state := m.standardPRListState(view)
		key := prPageKey(view, state, "")
		if _, exists := m.prPages[key]; exists {
			continue
		}
		if cached, ok := m.navigator.Views[m.viewName(view)]; ok {
			prs := make([]gh.PR, 0, len(cached.Numbers))
			for _, number := range cached.Numbers {
				if pr, exists := byNumber[number]; exists {
					prs = append(prs, pr)
				}
			}
			m.prPages[key] = prPageState{prs: prs, total: cached.TotalCount, loaded: true}
			continue
		}
		var prs []gh.PR
		for _, pr := range m.navigator.PRs {
			if matchesListState(pr, state) && m.matchesView(pr, view) {
				prs = append(prs, pr)
			}
		}
		if len(prs) > 0 {
			m.prPages[key] = prPageState{prs: prs, total: len(prs), loaded: true}
			m.navigator.SetView(m.viewName(view), prs, len(prs), m.navigator.FetchedAt)
		}
	}
}

func prPageKey(view prView, state prListState, filter string) string {
	return fmt.Sprintf("%d:%d:%s", view, state, strings.Join(strings.Fields(strings.ToLower(filter)), " "))
}

func splitPRFilter(query string) (server, local string) {
	serverTokens, localTokens := []string{}, []string{}
	tokens := strings.Fields(query)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if strings.HasPrefix(token, "(") {
			// GitHub's issue search understands neither OR nor grouping: it
			// matches the punctuation as free text and returns nothing, so
			// groups are evaluated locally instead.
			end := groupEnd(tokens, i)
			localTokens = append(localTokens, tokens[i:end+1]...)
			i = end
			continue
		}
		key, value, structured := strings.Cut(strings.ToLower(token), ":")
		if structured && (key == "ci" || key == "merge") {
			localTokens = append(localTokens, token)
			continue
		}
		if structured && (key == "is" || key == "state") && (value == "open" || value == "closed") {
			continue
		}
		serverTokens = append(serverTokens, token)
	}
	return strings.Join(serverTokens, " "), strings.Join(localTokens, " ")
}

// groupEnd returns the index of the token closing the parenthesized group
// that starts at i, or the last token when the group is never closed.
func groupEnd(tokens []string, i int) int {
	for ; i < len(tokens); i++ {
		if strings.HasSuffix(tokens[i], ")") {
			return i
		}
	}
	return len(tokens) - 1
}

// prViewSearch builds the GitHub search for a tab: the requested state, the
// view's own query (minus its state tokens, which state already carries), and
// the server-side part of the user's filter.
func (m Model) prViewSearch(view prView, state prListState, filter string) string {
	terms := []string{"is:" + strings.ToLower(state.String())}
	if query, _ := splitPRFilter(m.viewDef(view).Query); query != "" {
		terms = append(terms, query)
	}
	server, _ := splitPRFilter(filter)
	if server != "" {
		terms = append(terms, server)
	}
	return strings.Join(terms, " ")
}

func (m *Model) requestPRPage(reset bool) tea.Cmd {
	if m.prPages == nil {
		m.prPages = map[string]prPageState{}
	}
	key := m.activePRPage
	page := m.prPages[key]
	if page.loading || (!reset && (!page.loaded || !page.hasNext)) {
		return nil
	}
	cursor, appendPage := page.endCursor, true
	if reset {
		cursor, appendPage = "", false
	}
	page.loading = true
	m.prPages[key] = page
	m.listRefreshing = true
	m.githubStatus = "GitHub: fetching " + m.viewName(m.prView) + " pull requests…"
	return tea.Batch(fetchPRList(m.client, m.prListGeneration, key, m.prViewSearch(m.prView, m.prListState, m.filterQuery), cursor, appendPage), m.startSpinner())
}

func (m *Model) applyPRViewState(selectedNumber int) tea.Cmd {
	m.seedPRPages()
	desired := m.desiredPRListState()
	key := prPageKey(m.prView, desired, m.filterQuery)
	if key != m.activePRPage {
		m.prListGeneration++
		m.activePRPage = key
		m.prPreviewLoading = map[int]bool{}
		m.prPreviewLoaded = map[int]bool{}
	}
	m.prListState = desired
	m.applyPRFilters(selectedNumber)
	if page := m.prPages[key]; page.loading {
		m.listRefreshing = true
		m.githubStatus = "GitHub: fetching " + m.viewName(m.prView) + " pull requests…"
		return m.sync()
	} else if page.fresh {
		m.listRefreshing = false
		m.githubStatus = "GitHub: cached " + m.viewName(m.prView) + " page"
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
	return matchesPRFilter(pr, query, m.viewerLogin)
}

func hasLogin(users []gh.PRUser, login string) bool {
	if login == "" {
		return false
	}
	for _, user := range users {
		if strings.EqualFold(user.Login, login) {
			return true
		}
	}
	return false
}

// maxPRRowCacheEntries bounds the render cache; rows key on their full render
// inputs, so stale entries are merely unused, never wrong.
const maxPRRowCacheEntries = 512

func (m *Model) applyPRFilters(selectedNumber int) {
	if m.prRowCache == nil {
		m.prRowCache = map[prRowCacheKey][]string{}
	} else if len(m.prRowCache) > maxPRRowCacheEntries {
		// ponytail: full reset over LRU — refilling a screenful is cheap.
		clear(m.prRowCache)
	}
	if m.collapsedStacks == nil {
		m.collapsedStacks = map[string]bool{}
	}
	page, paged := m.prPages[m.activePRPage]
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
	m.allPRs = m.withLocalPR(source)
	m.recomputeViewCounts(page, paged)
	m.filteredPRs = make([]gh.PR, 0, len(m.allPRs))
	_, localFilter := splitPRFilter(m.filterQuery)
	// Server rows are trusted for the qualifiers GitHub ran; whatever it
	// could not evaluate (OR groups, ci:, merge:) is still ours to apply.
	_, viewLocal := splitPRFilter(m.viewDef(m.prView).Query)
	for _, pr := range m.allPRs {
		if page.loaded && pr.Number > 0 && sourceNumbers[pr.Number] {
			if viewLocal != "" && !matchesPRFilter(pr, viewLocal, m.viewerLogin) {
				continue
			}
			if localFilter == "" || matchesPRFilter(pr, localFilter, m.viewerLogin) {
				m.filteredPRs = append(m.filteredPRs, pr)
			}
			continue
		}
		if matchesListState(pr, m.prListState) && m.matchesView(pr, m.prView) && matchesPRFilter(pr, m.filterQuery, m.viewerLogin) {
			m.filteredPRs = append(m.filteredPRs, pr)
		}
	}
	slices.SortFunc(m.filteredPRs, func(a, b gh.PR) int { return a.Number - b.Number })
	if m.prListState == closedPRListState {
		m.prStacks = singlePRStacks(m.filteredPRs)
	} else {
		m.prStacks = buildPRStacks(m.filteredPRs)
	}
	m.openPRs = make([]gh.PR, 0, len(m.filteredPRs))
	for _, stack := range m.prStacks {
		entries := stack.entries
		if len(entries) > 1 && m.collapsedStacks[stack.id] {
			entries = entries[:1]
		}
		for _, entry := range entries {
			m.openPRs = append(m.openPRs, entry.pr)
		}
	}
	m.restorePRSelection(selectedNumber)
}

// recomputeViewCounts refreshes the per-view PR counts shown in the tab bar,
// preferring a loaded page's total, then the navigator, then the local set.
func (m *Model) recomputeViewCounts(page prPageState, paged bool) {
	m.viewCounts = make([]int, len(m.views))
	m.viewCountKnown = make([]bool, len(m.views))
	for view := prView(0); int(view) < len(m.views); view++ {
		state := m.standardPRListState(view)
		_, localOnly := splitPRFilter(m.viewDef(view).Query)
		cached, ok := m.prPages[prPageKey(view, state, "")]
		switch {
		case ok && cached.loaded && localOnly == "":
			m.viewCounts[view], m.viewCountKnown[view] = cached.total, true
		case ok && cached.loaded:
			// The server could not evaluate part of this view, so its total
			// counts rows the tab filters out. Count what was loaded.
			for _, pr := range cached.prs {
				if matchesListState(pr, state) && m.matchesView(pr, view) {
					m.viewCounts[view]++
				}
			}
			m.viewCountKnown[view] = true
		case page.loaded:
			for _, pr := range m.navigator.PRs {
				if matchesListState(pr, state) && m.matchesView(pr, view) {
					m.viewCounts[view]++
				}
			}
		}
	}
	if !paged || !page.loaded {
		for view := prView(0); int(view) < len(m.views); view++ {
			// A cached page already produced this view's exact total; adding
			// the allPRs fallback on top would double-count it.
			if m.viewCountKnown[view] {
				continue
			}
			state := m.standardPRListState(view)
			for _, pr := range m.allPRs {
				if matchesListState(pr, state) && m.matchesView(pr, view) {
					m.viewCounts[view]++
				}
			}
			m.viewCountKnown[view] = true
		}
	}
	if m.localAvailable && page.loaded {
		local := gh.PR{State: "LOCAL", Author: gh.PRUser{Login: m.viewerLogin}}
		for view := prView(0); int(view) < len(m.views); view++ {
			if m.matchesView(local, view) {
				m.viewCounts[view]++
			}
		}
	}
	m.viewCountsValid = true
}

func singlePRStacks(prs []gh.PR) []prStack {
	stacks := make([]prStack, len(prs))
	for i, pr := range prs {
		stacks[i] = prStack{id: fmt.Sprintf("pr:%d", pr.Number), order: i, entries: []stackEntry{{pr: pr}}}
	}
	return stacks
}

func buildPRStacks(prs []gh.PR) []prStack {
	if len(prs) == 0 {
		return nil
	}
	head := make(map[string]int, len(prs))
	for i, pr := range prs {
		if pr.HeadRefName == "" {
			continue
		}
		if _, exists := head[pr.HeadRefName]; exists {
			head[pr.HeadRefName] = -1 // ambiguous branch names must not invent a parent
		} else {
			head[pr.HeadRefName] = i
		}
	}
	parents := make([]int, len(prs))
	children := make([][]int, len(prs))
	for i := range parents {
		parents[i] = -1
		if parent, ok := head[prs[i].BaseRefName]; ok && parent >= 0 && parent != i {
			parents[i] = parent
			children[parent] = append(children[parent], i)
		}
	}
	visited := make([]bool, len(prs))
	stacks := make([]prStack, 0, len(prs))
	var addStack func(int)
	addStack = func(root int) {
		if visited[root] {
			return
		}
		stack := prStack{order: root}
		var walk func(int, int)
		walk = func(index, depth int) {
			if visited[index] {
				return
			}
			visited[index] = true
			if index < stack.order {
				stack.order = index
			}
			stack.entries = append(stack.entries, stackEntry{pr: prs[index], depth: depth})
			for _, child := range children[index] {
				walk(child, depth+1)
			}
		}
		walk(root, 0)
		rootPR := stack.entries[0].pr
		stack.id = fmt.Sprintf("pr:%d", rootPR.Number)
		if rootPR.Number == 0 {
			stack.id = "branch:" + rootPR.HeadRefName
		}
		if rootPR.Number > 0 {
			stack.title = fmt.Sprintf("#%d", rootPR.Number)
		} else {
			stack.title = "Local PR"
		}
		stacks = append(stacks, stack)
	}
	for i, parent := range parents {
		if parent == -1 {
			addStack(i)
		}
	}
	for i := range prs {
		addStack(i) // cycle/duplicate safety
	}
	sort.SliceStable(stacks, func(i, j int) bool { return stacks[i].order < stacks[j].order })
	return stacks
}

func (m Model) stackForPR(number int) (prStack, bool) {
	for _, stack := range m.prStacks {
		if len(stack.entries) < 2 {
			continue
		}
		for _, entry := range stack.entries {
			if entry.pr.Number == number {
				return stack, true
			}
		}
	}
	return prStack{}, false
}

// matchesPRFilter evaluates a GitHub-search-like query against one PR.
// Parenthesized alternatives — "(assignee:@me OR review-requested:@me)" —
// match when any alternative matches; everything else is ANDed.
func matchesPRFilter(pr gh.PR, query, viewer string) bool {
	matcher := prFilterMatcher{pr: pr, viewer: viewer}
	tokens := strings.Fields(strings.ToLower(query))
	for i := 0; i < len(tokens); i++ {
		if !strings.HasPrefix(tokens[i], "(") {
			if !matcher.token(tokens[i]) {
				return false
			}
			continue
		}
		alternatives, next := filterGroup(tokens, i)
		i = next
		matched := false
		for _, alternative := range alternatives {
			if matcher.token(alternative) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// filterGroup collects the alternatives of a parenthesized group starting at
// tokens[i], returning them and the index of its last token. An unclosed
// group runs to the end of the query.
func filterGroup(tokens []string, i int) ([]string, int) {
	end := groupEnd(tokens, i)
	var alternatives []string
	for _, token := range tokens[i : end+1] {
		token = strings.TrimSuffix(strings.TrimPrefix(token, "("), ")")
		if token != "" && token != "or" {
			alternatives = append(alternatives, token)
		}
	}
	return alternatives, end
}

// prFilterMatcher evaluates single filter tokens, building the free-text
// haystack only when a token needs it.
type prFilterMatcher struct {
	pr            gh.PR
	viewer        string
	haystack      string
	haystackBuilt bool
}

func (f *prFilterMatcher) token(token string) bool {
	pr, viewer := f.pr, f.viewer
	key, value, structured := strings.Cut(token, ":")
	if structured {
		me := value == "@me"
		if me {
			if viewer == "" {
				return false
			}
			value = strings.ToLower(viewer)
		}
		switch key {
		case "is", "state":
			switch value {
			case "open":
				return strings.EqualFold(pr.State, "OPEN")
			case "closed":
				// Match GitHub search semantics: is:closed covers merged
				// PRs too, like matchesListState's closed bucket.
				return strings.EqualFold(pr.State, "CLOSED") || strings.EqualFold(pr.State, "MERGED")
			case "draft":
				return key == "is" && pr.IsDraft
			case "pr":
				return key == "is"
			default:
				return false
			}
		case "author":
			return strings.EqualFold(pr.Author.Login, value)
		case "assignee":
			return hasLogin(pr.Assignees, value)
		case "review-requested":
			return me && pr.ViewerReviewRequested || hasLogin(pr.ReviewRequests, value)
		case "label":
			for _, label := range pr.Labels {
				if strings.EqualFold(label.Name, value) {
					return true
				}
			}
			return false
		case "draft":
			return (value == "true") == pr.IsDraft
		case "ci":
			return prCIHealth(pr) == value
		case "merge":
			conflicting := pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY"
			return value == "conflicting" && conflicting
		}
	}
	if !f.haystackBuilt {
		f.haystack = strings.ToLower(fmt.Sprintf("#%d %s %s %s %s", pr.Number, pr.Title, pr.HeadRefName, pr.BaseRefName, pr.Author.Login))
		for _, label := range pr.Labels {
			f.haystack += " " + strings.ToLower(label.Name)
		}
		f.haystackBuilt = true
	}
	return strings.Contains(f.haystack, token)
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

func (m Model) selectedPR() *gh.PR {
	if m.prCursor < 0 || m.prCursor >= len(m.openPRs) {
		return nil
	}
	return &m.openPRs[m.prCursor]
}

func (m Model) selectedPRNumber() int {
	if pr := m.selectedPR(); pr != nil {
		return pr.Number
	}
	return 0
}

func (m *Model) restorePRSelection(number int) {
	if number != 0 {
		for i := range m.openPRs {
			if m.openPRs[i].Number == number {
				m.prCursor = i
				return
			}
		}
	}
	if len(m.openPRs) == 0 {
		m.prCursor = 0
	} else if m.prCursor >= len(m.openPRs) {
		m.prCursor = len(m.openPRs) - 1
	}
}

func (m *Model) ensureSelectedPRPreview() tea.Cmd {
	pr := m.selectedPR()
	if pr == nil || pr.Number <= 0 || pr.PreviewLoaded {
		return nil
	}
	if m.prPreviewLoading == nil {
		m.prPreviewLoading = map[int]bool{}
	}
	if m.prPreviewLoaded == nil {
		m.prPreviewLoaded = map[int]bool{}
	}
	if m.prPreviewLoading[pr.Number] || m.prPreviewLoaded[pr.Number] {
		return nil
	}
	m.prPreviewLoading[pr.Number] = true
	m.status = fmt.Sprintf("loading PR #%d preview…", pr.Number)
	return fetchPRPreview(m.client, pr.Number, m.prListGeneration)
}

func (m Model) renderPRListHeader() string {
	tabs := make([]string, 0, len(m.views))
	activeBg := lipgloss.Color(cSelectedBg)
	for view := prView(0); int(view) < len(m.views); view++ {
		name, count := m.viewName(view), "?"
		if !m.viewCountsValid || (int(view) < len(m.viewCountKnown) && m.viewCountKnown[view]) {
			count = strconv.Itoa(m.viewCount(view))
		}
		border, content := stMuted, stMuted
		if view == m.prView {
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
	if m.filterEditing {
		filter = stAccent.Render(" ") + stFg.Render(m.filterQuery+"▌") + stMuted.Render(" · Enter search · Esc cancel")
	} else if m.filterQuery != "" {
		filter = stAccent.Render(" ") + stFg.Render(m.filterQuery) + stMuted.Render(" · Esc clear")
	}
	metrics := []string{}
	if page, ok := m.prPages[m.activePRPage]; ok && page.loaded {
		metrics = append(metrics, fmt.Sprintf("%d/%d loaded", len(page.prs), page.total))
	}
	metrics = append(metrics, fmt.Sprintf("%d listed", len(m.filteredPRs)), "⎇ "+m.currentBranch)
	line2 := filter + stMuted.Render("   · "+strings.Join(metrics, " · "))
	return m.withLogo(lipgloss.JoinVertical(lipgloss.Left, append(tabRows, line2)...))
}

func (m Model) viewCount(view prView) int {
	if m.viewCountsValid && view >= 0 && int(view) < len(m.viewCounts) {
		return m.viewCounts[view]
	}
	state := m.standardPRListState(view)
	count := 0
	for _, pr := range m.allPRs {
		if matchesListState(pr, state) && m.matchesView(pr, view) {
			count++
		}
	}
	return count
}

func (m Model) buildPRPreview() string {
	pr := m.selectedPR()
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
	if pr.ReviewDecision != "" {
		lines = append(lines, "  "+reviewSummary(pr.ReviewDecision))
	}
	if !pr.PreviewLoaded && pr.Number > 0 {
		lines = append(lines, "  "+stMuted.Render("loading preview details…"))
	}
	lines = append(lines,
		"",
		stBold.Render("Size"),
		stFg.Render(fmt.Sprintf("  %d files   ", pr.ChangedFiles))+stGreenF.Render(fmt.Sprintf("+%d", pr.Additions))+stFg.Render("   ")+stRedF.Render(fmt.Sprintf("-%d", pr.Deletions))+stFg.Render(fmt.Sprintf("   %d commits", pr.CommitCount)),
		stFg.Render(fmt.Sprintf("  %d comments", pr.CommentCount)),
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
		lines = append(lines, "  "+stMuted.Render("updated "+shortTS(pr.UpdatedAt)))
	}
	lines = append(lines, "")
	if pr.Number == 0 {
		lines = append(lines, "  "+stMuted.Render("(local PR has no GitHub conversation yet)"))
	} else {
		body := pr.Body
		if strings.TrimSpace(body) == "" {
			body = "(no description provided)"
		}
		header := m.userIcon(pr.Author.Login) + stMuted.Render(" @"+pr.Author.Login+" · description · "+shortTS(pr.CreatedAt))
		lines = append(lines, cardLines(header, previewMarkdown(body, width-7, 10), false, width, cCloudBorder)...)
		if len(pr.Conversation) > 0 {
			comment := pr.Conversation[0]
			header = m.userIcon(comment.Author.Login) + stMuted.Render(" @"+comment.Author.Login+" · comment · "+shortTS(comment.CreatedAt))
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

func checkCounts(checks []gh.PRCheck) (pending, failed, passed int) {
	for _, check := range checks {
		conclusion := strings.ToUpper(check.Conclusion)
		state := strings.ToUpper(check.State)
		status := strings.ToUpper(check.Status)
		switch {
		case conclusion == "FAILURE" || conclusion == "CANCELLED" || conclusion == "TIMED_OUT" || conclusion == "ACTION_REQUIRED" || conclusion == "STARTUP_FAILURE" || conclusion == "STALE" || state == "FAILURE" || state == "ERROR":
			failed++
		case status != "COMPLETED" && conclusion == "" && state != "SUCCESS":
			pending++
		default:
			passed++
		}
	}
	return pending, failed, passed
}

func checkHealth(checks []gh.PRCheck) (string, int) {
	pending, failed, passed := checkCounts(checks)
	switch {
	case failed > 0:
		return "failed", failed
	case pending > 0:
		return "pending", pending
	case passed > 0:
		return "passed", passed
	default:
		return "none", 0
	}
}

func checkRollupState(checks []gh.PRCheck) string {
	health, _ := checkHealth(checks)
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

func prCIHealth(pr gh.PR) string {
	if pr.PreviewLoaded || len(pr.Checks) > 0 {
		health, _ := checkHealth(pr.Checks)
		return health
	}
	switch strings.ToUpper(pr.CheckRollupState) {
	case "SUCCESS":
		return "passed"
	case "FAILURE", "ERROR":
		return "failed"
	case "PENDING", "EXPECTED", "IN_PROGRESS":
		return "pending"
	default:
		return "unknown"
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
	pending, failed, passed := checkCounts(pr.Checks)
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
		switch prCIHealth(pr) {
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
	health, count := checkHealth(checks)
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
	if len(m.openPRs) == 0 {
		message := stMuted.Render("(no pull requests in this view)")
		if m.listRefreshing {
			message = stMuted.Render("fetching " + strings.ToLower(m.prListState.Label()) + " pull requests…")
		}
		return lipgloss.Place(max(1, m.list.Width), max(1, m.list.Height), lipgloss.Center, lipgloss.Center, message), 0
	}
	// openPRs is derived from prStacks in applyPRFilters, so a non-empty
	// list always has stacks.
	stacks := m.prStacks
	lines := make([]string, 0, len(m.openPRs)*3+len(stacks))
	selectedLine, openIndex := 0, 0
	for _, stack := range stacks {
		entries := stack.entries
		grouped := len(entries) > 1
		if grouped {
			collapsed := m.collapsedStacks[stack.id]
			arrow := "▾"
			if collapsed {
				arrow, entries = "▸", entries[:1]
			}
			header := stMuted.Render(arrow+" ") + stBold.Render(stack.title) + stMuted.Render(fmt.Sprintf(" · %d PRs", len(stack.entries)))
			lines = append(lines, ansi.Truncate(header, max(10, m.list.Width), "…"))
		}
		for i, entry := range entries {
			prefix := ""
			if grouped {
				marker := "├ "
				if i == len(entries)-1 {
					marker = "└ "
				}
				prefix = strings.Repeat("  ", entry.depth) + marker
			}
			selected := openIndex == m.prCursor
			if selected {
				selectedLine = len(lines)
			}
			if selected {
				lines = append(lines, m.renderPRRow(entry.pr, true, prefix)...)
			} else {
				lines = append(lines, m.cachedPRRow(entry.pr, prefix)...)
			}
			openIndex++
		}
	}
	return strings.Join(lines, "\n"), selectedLine
}

func (m *Model) cachedPRRow(pr gh.PR, prefix string) []string {
	if m.prRowCache == nil {
		m.prRowCache = map[prRowCacheKey][]string{}
	}
	health, count := checkHealth(pr.Checks)
	key := prRowCacheKey{
		number: pr.Number, width: max(10, m.list.Width), additions: pr.Additions, deletions: pr.Deletions, checkCount: count,
		prefix: prefix, state: pr.State, title: pr.Title, author: pr.Author.Login, base: pr.BaseRefName, head: pr.HeadRefName,
		mergeable: pr.Mergeable, mergeState: pr.MergeStateStatus, checkHealth: health, rollup: pr.CheckRollupState,
		draft: pr.IsDraft, previewLoaded: pr.PreviewLoaded, current: m.isCurrentTargetPR(pr),
	}
	if rows, ok := m.prRowCache[key]; ok {
		return rows
	}
	rows := m.renderPRRow(pr, false, prefix)
	m.prRowCache[key] = rows
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
// only changes the background and padding, never the content.
func (m Model) prRowSegments(pr gh.PR, prefix string) (line, meta []rowSegment) {
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
	if pr.Number > 0 {
		if mergeText, mergeStyle := mergeState(pr); mergeText != "" {
			meta = append(meta, rowSegment{text: " · ", style: stMuted}, rowSegment{text: mergeText, style: mergeStyle})
		}
		checkText, checkStyle := prCheckState(pr)
		meta = append(meta, rowSegment{text: " · ", style: stMuted}, rowSegment{text: checkText, style: checkStyle})
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
	lineSegments, metaSegments := m.prRowSegments(pr, prefix)
	width := max(10, m.list.Width)
	current := m.isCurrentTargetPR(pr)
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
		foreground = contrastingLabelForeground(rgb)
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(background)).Foreground(lipgloss.Color(foreground)).Padding(0, 1).Render(label.Name)
}

func contrastingLabelForeground(background uint64) string {
	const dark uint64 = 0x0d1117
	bgLuminance := relativeLuminance(background)
	whiteContrast := (1.0 + 0.05) / (bgLuminance + 0.05)
	darkLuminance := relativeLuminance(dark)
	darkContrast := (math.Max(bgLuminance, darkLuminance) + 0.05) / (math.Min(bgLuminance, darkLuminance) + 0.05)
	if darkContrast > whiteContrast {
		return "#0d1117"
	}
	return "#ffffff"
}

func relativeLuminance(rgb uint64) float64 {
	channel := func(value uint64) float64 {
		v := float64(value) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	r := channel((rgb >> 16) & 0xff)
	g := channel((rgb >> 8) & 0xff)
	b := channel(rgb & 0xff)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// stepView cycles the selected tab, tolerating an index left out of range by
// a shortened view list.
func (m Model) stepView(delta int) prView {
	count := len(m.views)
	if count == 0 {
		return 0
	}
	next := (int(m.prView)%count + count + delta%count) % count
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
