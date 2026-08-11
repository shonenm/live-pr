package tui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	if m.prView == closedPRsView {
		return closedPRListState
	}
	return openPRListState
}

func (m *Model) applyPRViewState(selectedNumber int) tea.Cmd {
	desired := m.desiredPRListState()
	if desired == m.prListState {
		m.applyPRFilters(selectedNumber)
		return m.sync()
	}
	m.prListState = desired
	m.prListGeneration++
	m.prPreviewLoading = map[int]bool{}
	m.prPreviewLoaded = map[int]bool{}
	m.applyPRFilters(selectedNumber)
	if m.navigator.FetchedStates[desired.String()] {
		m.listRefreshing = false
		m.githubStatus = "GitHub: cached PR list"
		return m.sync()
	}
	m.listRefreshing = true
	m.githubStatus = fmt.Sprintf("GitHub: refreshing %s pull requests…", desired.Label())
	return tea.Batch(fetchPRList(m.prListGeneration, desired), m.sync(), m.startSpinner())
}

func (v prView) String() string {
	switch v {
	case assignedView:
		return "Assigned"
	case reviewRequestedView:
		return "Review requested"
	case allPRsView:
		return "All"
	case authoredView:
		return "Authored"
	case needsMeView:
		return "Needs me"
	case closedPRsView:
		return "Closed"
	default:
		return "All"
	}
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

func replacePRsForState(existing, fetched []gh.PR, state prListState) []gh.PR {
	fetchedNumbers := make(map[int]bool, len(fetched))
	for _, pr := range fetched {
		fetchedNumbers[pr.Number] = true
	}
	preserved := make([]gh.PR, 0, len(existing))
	for _, pr := range existing {
		if !matchesListState(pr, state) && !fetchedNumbers[pr.Number] {
			preserved = append(preserved, pr)
		}
	}
	if state == openPRListState {
		return append(append([]gh.PR(nil), fetched...), preserved...)
	}
	return append(preserved, fetched...)
}

func (m Model) matchesView(pr gh.PR, view prView) bool {
	if pr.Number == 0 {
		return view != closedPRsView
	}
	if view == allPRsView || view == closedPRsView {
		return true
	}
	authored := strings.EqualFold(pr.Author.Login, m.viewerLogin)
	assigned := hasLogin(pr.Assignees, m.viewerLogin)
	reviewRequested := pr.ViewerReviewRequested || hasLogin(pr.ReviewRequests, m.viewerLogin)
	switch view {
	case reviewRequestedView:
		return reviewRequested
	case assignedView:
		return assigned
	case authoredView:
		return authored
	case needsMeView:
		return assigned || reviewRequested
	default:
		return true
	}
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

func (m *Model) applyPRFilters(selectedNumber int) {
	if m.collapsedStacks == nil {
		m.collapsedStacks = map[string]bool{}
	}
	m.allPRs = m.withLocalPR(m.navigator.PRs)
	clear(m.viewCounts[:])
	for view := assignedView; view < prViewCount; view++ {
		state := openPRListState
		if view == closedPRsView {
			state = closedPRListState
		}
		for _, pr := range m.allPRs {
			if matchesListState(pr, state) && m.matchesView(pr, view) {
				m.viewCounts[view]++
			}
		}
	}
	m.viewCountsValid = true
	m.filteredPRs = make([]gh.PR, 0, len(m.allPRs))
	for _, pr := range m.allPRs {
		if matchesListState(pr, m.prListState) && m.matchesView(pr, m.prView) && matchesPRFilter(pr, m.filterQuery, m.viewerLogin) {
			m.filteredPRs = append(m.filteredPRs, pr)
		}
	}
	m.prStacks = buildPRStacks(m.filteredPRs)
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
		stack.title = rootPR.HeadRefName
		if stack.title == "" {
			stack.title = rootPR.Title
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

func matchesPRFilter(pr gh.PR, query, viewer string) bool {
	for _, token := range strings.Fields(strings.ToLower(query)) {
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
				case "open", "closed":
					if !strings.EqualFold(pr.State, value) {
						return false
					}
				case "draft":
					if key != "is" || !pr.IsDraft {
						return false
					}
				case "pr":
					if key != "is" {
						return false
					}
				default:
					return false
				}
				continue
			case "author":
				if !strings.EqualFold(pr.Author.Login, value) {
					return false
				}
				continue
			case "assignee":
				if !hasLogin(pr.Assignees, value) {
					return false
				}
				continue
			case "review-requested":
				if !(me && pr.ViewerReviewRequested) && !hasLogin(pr.ReviewRequests, value) {
					return false
				}
				continue
			case "label":
				matched := false
				for _, label := range pr.Labels {
					matched = matched || strings.EqualFold(label.Name, value)
				}
				if !matched {
					return false
				}
				continue
			case "draft":
				if (value == "true") != pr.IsDraft {
					return false
				}
				continue
			case "ci":
				if prCIHealth(pr) != value {
					return false
				}
				continue
			case "merge":
				conflicting := pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY"
				if value != "conflicting" || !conflicting {
					return false
				}
				continue
			}
		}
		haystack := strings.ToLower(fmt.Sprintf("#%d %s %s %s %s", pr.Number, pr.Title, pr.HeadRefName, pr.BaseRefName, pr.Author.Login))
		for _, label := range pr.Labels {
			haystack += " " + strings.ToLower(label.Name)
		}
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
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
	return fetchPRPreview(pr.Number, m.prListGeneration)
}

// sync rebuilds both panes for the current tab and selection.
func (m Model) renderPRListHeader() string {
	views := make([]string, 0, prViewCount)
	for view := assignedView; view < prViewCount; view++ {
		label := fmt.Sprintf("%s %d", view, m.viewCount(view))
		style := stMuted
		if view == m.prView {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg)).Background(lipgloss.Color(cSelectedBg)).Bold(true).Padding(0, 1)
		}
		views = append(views, style.Render(label))
	}
	line1 := stBold.Render("Pull requests") + "  " + strings.Join(views, " ")
	filter := stMuted.Render("/ filter (is:closed) · [/] views · space stacks")
	if m.filterEditing {
		filter = stAccent.Render("Filter: ") + stFg.Render(m.filterQuery+"▌")
	} else if m.filterQuery != "" {
		filter = stAccent.Render("Filter: ") + stFg.Render(m.filterQuery) + stMuted.Render(" · Esc clear")
	}
	line2 := filter + stMuted.Render(fmt.Sprintf("   · %d listed · current %s", len(m.filteredPRs), m.currentBranch))
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).Render(strings.Repeat("─", max(0, m.w)))
	return lipgloss.JoinVertical(lipgloss.Left, ansi.Truncate(line1, max(1, m.w), "…"), ansi.Truncate(line2, max(1, m.w), "…"), rule)
}

func (m Model) viewCount(view prView) int {
	if m.viewCountsValid && view >= 0 && view < prViewCount {
		return m.viewCounts[view]
	}
	state := openPRListState
	if view == closedPRsView {
		state = closedPRListState
	}
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
	lines := []string{
		stMuted.Render(identifier) + "  " + stBold.Render(pr.Title),
		stMuted.Render(pr.BaseRefName + " ← " + pr.HeadRefName),
		"",
		stBold.Render("Status"),
		"  " + mergeSummary(*pr) + "   " + prCheckSummary(*pr),
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
		"  "+previewPeople(*pr),
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
		header := stMuted.Render("💬 @" + pr.Author.Login + " · description · " + shortTS(pr.CreatedAt))
		lines = append(lines, cardLines(header, previewMarkdown(body, width-7, 10), false, width, cCloudBorder)...)
		if len(pr.Conversation) > 0 {
			comment := pr.Conversation[0]
			header = stMuted.Render("💬 @" + comment.Author.Login + " · comment · " + shortTS(comment.CreatedAt))
			lines = append(lines, "")
			lines = append(lines, cardLines(header, previewMarkdown(comment.Body, width-7, 5), false, width, cCloudBorder)...)
		}
	}
	return strings.Join(lines, "\n")
}

func previewPeople(pr gh.PR) string {
	parts := []string{}
	if pr.Author.Login != "" {
		parts = append(parts, "author @"+pr.Author.Login)
	}
	if len(pr.Assignees) > 0 {
		users := make([]string, 0, len(pr.Assignees))
		for _, user := range pr.Assignees {
			users = append(users, "@"+user.Login)
		}
		parts = append(parts, "assigned "+strings.Join(users, " "))
	}
	if len(parts) == 0 {
		return stMuted.Render("unassigned")
	}
	return stFg.Render(strings.Join(parts, " · "))
}

func reviewSummary(decision string) string {
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

func mergeSummary(pr gh.PR) string {
	if pr.Number == 0 {
		return stMuted.Render("local")
	}
	state := strings.ToLower(strings.ReplaceAll(pr.MergeStateStatus, "_", " "))
	if pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return stRedF.Render("conflicts")
	}
	if state == "" {
		state = strings.ToLower(pr.Mergeable)
	}
	if state == "" {
		state = "merge unknown"
	}
	if pr.Mergeable == "MERGEABLE" && (pr.MergeStateStatus == "CLEAN" || pr.MergeStateStatus == "UNSTABLE") {
		return stGreenF.Render("mergeable")
	}
	switch pr.MergeStateStatus {
	case "BLOCKED":
		return stRedF.Render(state)
	case "BEHIND", "HAS_HOOKS":
		return stAttention.Render(state)
	default:
		return stMuted.Render(state)
	}
}

func checkHealth(checks []gh.PRCheck) (string, int) {
	if len(checks) == 0 {
		return "none", 0
	}
	failed, pending := 0, 0
	for _, check := range checks {
		conclusion := strings.ToUpper(check.Conclusion)
		state := strings.ToUpper(check.State)
		status := strings.ToUpper(check.Status)
		switch {
		case conclusion == "FAILURE" || conclusion == "CANCELLED" || conclusion == "TIMED_OUT" || conclusion == "ACTION_REQUIRED" || conclusion == "STARTUP_FAILURE" || conclusion == "STALE" || state == "FAILURE" || state == "ERROR":
			failed++
		case status != "COMPLETED" && conclusion == "" && state != "SUCCESS":
			pending++
		}
	}
	if failed > 0 {
		return "failed", failed
	}
	if pending > 0 {
		return "pending", pending
	}
	return "passed", len(checks)
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
	if !pr.PreviewLoaded && len(pr.Checks) == 0 {
		switch prCIHealth(pr) {
		case "passed":
			return stGreenF.Render("CI passed")
		case "failed":
			return stRedF.Render("CI failed")
		case "pending":
			return stAttention.Render("CI pending")
		default:
			return stMuted.Render("CI loading")
		}
	}
	return checkSummary(pr.Checks)
}

func checkSummary(checks []gh.PRCheck) string {
	health, count := checkHealth(checks)
	switch health {
	case "failed":
		return stRedF.Render(fmt.Sprintf("CI %d failed", count))
	case "pending":
		return stAttention.Render(fmt.Sprintf("CI %d pending", count))
	case "passed":
		return stGreenF.Render(fmt.Sprintf("CI %d passed", count))
	default:
		return stMuted.Render("CI no checks")
	}
}

func previewMarkdown(text string, width, maxLines int) string {
	lines := strings.Split(md.Render(text, width), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], stMuted.Render("…"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) buildPRList() string {
	content, _ := m.buildPRListRows()
	return content
}

func (m Model) buildPRListRows() (string, int) {
	if len(m.openPRs) == 0 {
		if m.listRefreshing {
			return stMuted.Render("fetching " + strings.ToLower(m.prListState.Label()) + " pull requests…"), 0
		}
		return stMuted.Render("(no pull requests in this view)"), 0
	}
	stacks := m.prStacks
	if len(stacks) == 0 {
		stacks = buildPRStacks(m.openPRs)
	}
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
			lines = append(lines, m.renderPRRow(entry.pr, selected, prefix)...)
			openIndex++
		}
	}
	return strings.Join(lines, "\n"), selectedLine
}

func (m Model) renderPRRow(pr gh.PR, selected bool, prefix string) []string {
	state := strings.ToLower(pr.State)
	if state == "" {
		state = "open"
	}
	identifier := fmt.Sprintf("#%d", pr.Number)
	owner := " · @" + pr.Author.Login
	if pr.IsDraft && state == "open" {
		state = "draft"
	}
	if pr.Number == 0 {
		state, identifier, owner = "local", "Local PR", ""
	}
	stateStyle := stMuted
	if state == "open" {
		stateStyle = stGreenF
	}
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	line := selectionBar(selected) + stMuted.Render(prefix+identifier) + " " + stBold.Render(pr.Title)
	meta := "  " + indent + stateStyle.Render(state)
	if pr.Number > 0 {
		meta += " · " + mergeSummary(pr) + " · " + prCheckSummary(pr)
	}
	meta += stMuted.Render(fmt.Sprintf(" · %s ← %s%s", pr.BaseRefName, pr.HeadRefName, owner))
	return []string{ansi.Truncate(line, max(10, m.list.Width), "…"), ansi.Truncate(meta, max(10, m.list.Width), "…"), ""}
}
func (m Model) renderPRMeta(pr gh.PR) string {
	assignees := stMuted.Render("👤 unassigned")
	if len(pr.Assignees) > 0 {
		users := make([]string, 0, len(pr.Assignees))
		for _, user := range pr.Assignees {
			users = append(users, "@"+user.Login)
		}
		assignees = stMuted.Render("👤 ") + stFg.Render(strings.Join(users, " "))
	}
	labels := stMuted.Render("🏷 no labels")
	if len(pr.Labels) > 0 {
		pills := make([]string, 0, len(pr.Labels))
		for _, label := range pr.Labels {
			pills = append(pills, labelPill(label))
		}
		labels = stMuted.Render("🏷 ") + strings.Join(pills, " ")
	}
	line := "  " + assignees + "   " + labels
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
