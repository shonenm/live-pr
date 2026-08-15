package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/embeddedterm"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func appendUniquePRs(existing, added []gh.PR) []gh.PR {
	result := append([]gh.PR(nil), existing...)
	index := make(map[int]int, len(existing)+len(added))
	for i, pr := range result {
		index[pr.Number] = i
	}
	for _, pr := range added {
		if i, ok := index[pr.Number]; ok {
			if !result[i].PreviewLoaded || pr.PreviewLoaded {
				result[i] = pr
			}
		} else {
			index[pr.Number] = len(result)
			result = append(result, pr)
		}
	}
	return result
}

func (m Model) handlePRListRefreshed(msg prListRefreshed) (Model, tea.Cmd) {
	if msg.generation != m.prListGeneration || msg.key != m.activePRPage {
		if page, ok := m.prPages[msg.key]; ok {
			page.loading = false
			m.prPages[msg.key] = page
		}
		return m, nil
	}
	if m.prPages == nil {
		m.prPages = map[string]prPageState{}
	}
	page := m.prPages[msg.key]
	page.loading = false
	m.listRefreshing = false
	if msg.err != nil {
		m.prPages[msg.key] = page
		m.githubStatus = "Offline · showing cached PR list"
		return m, m.sync()
	}
	selectedNumber := m.selectedPRNumber()
	if msg.appendPage {
		page.prs = appendUniquePRs(page.prs, msg.page.PRs)
	} else {
		page.prs = append([]gh.PR(nil), msg.page.PRs...)
	}
	page.total = msg.page.TotalCount
	page.endCursor = msg.page.PageInfo.EndCursor
	page.hasNext = msg.page.PageInfo.HasNextPage
	page.loaded, page.fresh = true, true
	m.prPages[msg.key] = page
	if !msg.appendPage {
		m.prPreviewLoading = map[int]bool{}
		m.prPreviewLoaded = map[int]bool{}
	}
	if msg.page.Repository != "" {
		m.repository = msg.page.Repository
		m.navigator.Repository = msg.page.Repository
	}
	if msg.page.ViewerLogin != "" {
		m.viewerLogin = msg.page.ViewerLogin
		m.navigator.ViewerLogin = msg.page.ViewerLogin
	}
	now := time.Now().UTC().Format(time.RFC3339)
	cacheUpdated := strings.TrimSpace(m.filterQuery) == "" && m.prListState == standardPRListState(m.prView)
	if cacheUpdated {
		m.navigator.PRs = appendUniquePRs(m.navigator.PRs, msg.page.PRs)
		m.navigator.SetView(m.prView.String(), page.prs, page.total, now)
		m.navigator.PrunePRs()
		m.navigator.FetchedAt = now
	}
	if m.screen == detailScreen && !m.remote && m.cache.PR == nil {
		for i := range msg.page.PRs {
			if isCurrentPR(msg.page.PRs[i], m.currentBranch) {
				m.cache.PR = &msg.page.PRs[i]
				m.localAvailable = false
				break
			}
		}
	}
	m.applyPRFilters(selectedNumber)
	if cacheUpdated {
		if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
			m.status = "PR list cache: " + err.Error()
		}
	}
	m.githubStatus = fmt.Sprintf("GitHub: %d of %d %s pull requests", len(page.prs), page.total, m.prView.String())
	_, localFilter := splitPRFilter(m.filterQuery)
	avatars := loadListAvatarColors(m.prListGeneration, msg.page.PRs)
	if page.hasNext && (len(msg.page.PRs) == 0 || localFilter != "" && len(m.openPRs) == 0) {
		return m, tea.Batch(m.sync(), avatars, m.requestPRPage(false))
	}
	return m, tea.Batch(m.sync(), avatars)

}

func (m Model) handleCurrentBranchPRLoaded(msg currentBranchPRLoaded) (Model, tea.Cmd) {
	if msg.err != nil {
		m.autoOpenCurrent = false
		if errors.Is(msg.err, gh.ErrPRNotFound) {
			m.localAvailable = true
			m.applyPRFilters(m.selectedPRNumber())
			return m, m.sync()
		}
		m.githubStatus = "Offline · current branch PR unavailable"
		return m, nil
	}
	if !isCurrentPR(msg.pr, m.currentBranch) {
		return m, nil
	}
	m.localAvailable = false
	m.navigator.PRs = upsertPR(m.navigator.PRs, msg.pr)
	if matchesListState(msg.pr, closedPRListState) {
		m.prView, m.prListState, m.listRefreshing = closedPRsView, closedPRListState, false
		m.prListGeneration++
		m.activePRPage = prPageKey(m.prView, m.prListState, "")
	}
	m.applyPRFilters(msg.pr.Number)
	if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
		m.status = "PR list cache: " + err.Error()
	}
	if m.screen != prListScreen || !m.autoOpenCurrent {
		return m, m.sync()
	}
	m.autoOpenCurrent = false
	st := store.ForBranch(m.root, m.currentBranch)
	cache, _ := gh.LoadCache(st.GitHubCache(), m.currentBranch)
	cache.PR = &msg.pr
	return m, tea.Batch(m.startLocalLoad(st, cache, &msg.pr), m.startSpinner())

}

func (m Model) handlePRPreviewLoaded(msg prPreviewLoaded) (Model, tea.Cmd) {
	if msg.generation != m.prListGeneration {
		return m, nil
	}
	selectedNumber := m.selectedPRNumber()
	if m.prPreviewLoading == nil {
		m.prPreviewLoading = map[int]bool{}
	}
	if m.prPreviewLoaded == nil {
		m.prPreviewLoaded = map[int]bool{}
	}
	delete(m.prPreviewLoading, msg.number)
	m.prPreviewLoaded[msg.number] = true
	if msg.err != nil {
		m.status = fmt.Sprintf("PR #%d preview: %v", msg.number, msg.err)
		return m, nil
	}
	for i := range m.navigator.PRs {
		if m.navigator.PRs[i].Number == msg.number {
			msg.pr.ViewerReviewRequested = m.navigator.PRs[i].ViewerReviewRequested
			m.navigator.PRs[i] = msg.pr
			break
		}
	}
	// A PR number lives on exactly one page, so stop scanning once found.
	// page.prs shares its backing array with the map entry, so the in-place
	// write is visible without reassigning m.prPages[key].
	for _, page := range m.prPages {
		done := false
		for i := range page.prs {
			if page.prs[i].Number == msg.number {
				c := msg.pr
				c.ViewerReviewRequested = page.prs[i].ViewerReviewRequested
				page.prs[i] = c
				done = true
				break
			}
		}
		if done {
			break
		}
	}
	m.applyPRFilters(selectedNumber)
	// Only persist when the visible row's preview loads; background prefetches
	// stay in memory and ride the next page-load/refresh save.
	onSelected := m.selectedPRNumber() == msg.number
	if onSelected {
		if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
			m.status = "PR list cache: " + err.Error()
		} else {
			m.status = ""
		}
	}
	return m, tea.Batch(m.sync(), loadListAvatarColors(m.prListGeneration, []gh.PR{msg.pr}))

}

func (m Model) handleDiffRendered(msg diffRendered) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	delete(m.diffPending, msg.key)
	if msg.err != nil {
		m.diffCache[msg.key] = msg.raw
	} else {
		m.diffCache[msg.key] = msg.output
	}
	if m.detailKey == msg.key {
		if msg.err != nil {
			m.status = msg.err.Error()
		} else if strings.HasPrefix(m.status, "diff display") {
			m.status = ""
		}
		m.detail.SetContent(m.diffCache[msg.key])
		m.detail.GotoTop()
	}
	return m, nil

}

func (m Model) handlePRActionDone(msg prActionDone) (Model, tea.Cmd) {
	m.prActionRunning = noPRAction
	if msg.err != nil {
		m.status = fmt.Sprintf("PR #%d: %v", msg.number, msg.err)
		return m, nil
	}
	if msg.action == mergePR || msg.action == closePR {
		if msg.action == mergePR {
			m.notice = fmt.Sprintf("Merge submitted for PR #%d", msg.number)
		} else {
			m.notice = fmt.Sprintf("Closed PR #%d", msg.number)
		}
		m.prListGeneration++
		if m.prPages == nil {
			m.prPages = map[string]prPageState{}
		}
		page := m.prPages[m.activePRPage]
		page.fresh, page.loading = false, false
		m.prPages[m.activePRPage] = page
		return m, m.requestPRPage(true)
	}
	m.close()
	next, err := New(m.version)
	if err != nil {
		m.status = "checkout reload: " + err.Error()
		return m, nil
	}
	if msg.pr.Number > 0 {
		cache := gh.NewCache(next.currentBranch)
		cache.PR, cache.ExplicitCheckout = &msg.pr, true
		if err := next.loadLocal(store.ForBranch(next.root, next.currentBranch), cache, &msg.pr); err != nil {
			m.status = "checkout reload: " + err.Error()
			return m, nil
		}
		if err := gh.SaveCache(next.cachePath, next.cache); err != nil {
			m.status = "checkout cache: " + err.Error()
			return m, nil
		}
	}
	next.w, next.h = m.w, m.h
	next.advanceAsyncGenerations(m)
	next.notice = fmt.Sprintf("Checked out PR #%d", msg.number)
	next.layout()
	return next, tea.Batch(next.Init(), next.sync())

}

func (m Model) handleBrowserDone(msg browserDone) (Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "browser: " + msg.err.Error()
	} else if msg.copied {
		m.notice = "URL copied to clipboard"
	} else if strings.HasPrefix(m.status, "browser:") {
		m.status = ""
	}
	return m, nil

}

func (m Model) handlePublishDone(msg publishDone) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	m.publishing = false
	if msg.err != nil {
		m.status = "publish: " + msg.err.Error()
		return m, nil
	}
	selectedKey := m.selectedConversationKey()
	if cache, err := gh.LoadCache(m.cachePath, m.head); err == nil {
		m.cache = cache
		m.invalidateConversation()
	}
	action := "updated"
	if msg.result.Created {
		action = "created"
	}
	m.status = ""
	m.githubStatus = "PR " + action + ": " + msg.result.PR.URL
	m.layout()
	m.restoreConversationSelection(selectedKey)
	return m, m.sync()

}

func (m Model) handleLocalLoaded(msg localLoaded) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	if msg.err != nil {
		m.refreshing = false
		m.status = msg.err.Error()
		return m, nil
	}
	m.applyLocal(msg.st, msg.data)
	cmds := []tea.Cmd{fetchGitHub(m.currentBranch, m.currentPRNumber(), m.targetGeneration), m.sync()}
	if m.diffTerminal != nil {
		cmds = append(cmds, m.diffTerminal.Init())
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleRemoteLoaded(msg remoteLoaded) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	m.refreshing = false
	selectedKey := m.selectedConversationKey()
	now := time.Now().UTC().Format(time.RFC3339)
	m.resetDetailCaches()
	m.cache.PR = &msg.pr
	if strings.TrimSpace(msg.pr.Title) != "" {
		m.title = msg.pr.Title
	}
	if msg.commentsErr == nil {
		m.cache.Comments = msg.comments
	}
	if msg.activitiesErr == nil {
		m.cache.Activities = msg.activities
	}
	m.cache.Reviews, m.cache.ReviewComments = msg.reviews, msg.reviewComments
	m.cache.FetchedAt = now
	m.invalidateConversation()
	m.navigator.SetSnapshot(gh.PRSnapshot{PR: msg.pr, Comments: m.cache.Comments, Activities: m.cache.Activities, FetchedAt: now})
	if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
		m.status = "PR list cache: " + err.Error()
	}
	if msg.refErr != nil {
		m.status = msg.refErr.Error()
		m.githubStatus = "GitHub: Conversation updated · review ref unavailable"
		m.restoreConversationSelection(selectedKey)
		return m, m.sync()
	}
	m.headRev = msg.headRef
	m.base, m.diffBase = msg.base, msg.diffBase
	m.reviewRange = m.diffBase + "..." + m.headRev
	m.commits, m.files = msg.commits, msg.files
	m.mergeReadiness, m.mergeReadinessErr = applyGitHubConflictFallback(msg.readiness, msg.readinessErr, msg.pr)
	m.fileCursor = 0
	m.status = ""
	stale := []string{}
	if msg.previewErr != nil {
		stale = append(stale, "metadata")
	}
	if msg.commentsErr != nil {
		stale = append(stale, "comments")
	}
	if msg.activitiesErr != nil {
		stale = append(stale, "activity")
	}
	if msg.readinessErr != nil {
		stale = append(stale, "merge readiness")
	}
	m.githubStatus = "GitHub: selected PR updated"
	if len(stale) > 0 {
		m.githubStatus += " · " + strings.Join(stale, "/") + " stale"
	}
	m.diffTerminal = embeddedterm.New(m.diffCommand, m.root, embeddedterm.Environment(m.reviewRange, m.diffBase, m.head, m.headRev, msg.pr.URL, "", m.reviewedMarksPath))
	m.layout()
	m.restoreConversationSelection(selectedKey)
	cmds := []tea.Cmd{m.sync(), m.nextCIPoll(), loadRichContent(m.targetGeneration, m.list.Width-7, m.cache.PR, m.cache.Comments, m.cache.Activities)}
	if m.diffTerminal != nil {
		cmds = append(cmds, m.diffTerminal.Init())
	}
	return m, tea.Batch(cmds...)

}

func (m Model) handleCIPolled(msg ciPolled) (Model, tea.Cmd) {
	// Errors carry no PR number, so the number check applies to successes only.
	if !m.ciPollTargetsCurrentPR(msg.generation) || (msg.err == nil && m.cache.PR.Number != msg.pr.Number) {
		return m, nil
	}
	if msg.err != nil {
		m.ciPollFailures++
		m.githubStatus = "GitHub: CI update unavailable · retrying…"
		return m, scheduleCIPoll(msg.generation, m.cache.PR.Number, m.ciPollFailures)
	}
	if msg.pr.HeadRefOID != m.cache.PR.HeadRefOID {
		m.githubStatus = "GitHub: PR head changed · refresh required"
		return m, nil
	}
	m.ciPollFailures = 0
	m.cache.PR.Checks = msg.pr.Checks
	m.cache.PR.CheckRollupState = checkRollupState(msg.pr.Checks)
	for i := range m.cache.PR.Commits {
		if m.cache.PR.Commits[i].OID == msg.pr.HeadRefOID {
			m.cache.PR.Commits[i].CheckRollupState = m.cache.PR.CheckRollupState
		}
	}
	m.cache.PR.PreviewLoaded = true
	m.invalidateConversation()
	m.navigator.PRs = upsertPR(m.navigator.PRs, *m.cache.PR)
	m.prRowCache = map[prRowCacheKey][]string{}
	m.githubStatus = "GitHub: CI updated now"
	m.layout()
	cmd := m.sync()
	if prCIHealth(*m.cache.PR) == "pending" {
		return m, tea.Batch(cmd, scheduleCIPoll(msg.generation, msg.pr.Number, 0))
	}
	return m, cmd
}

func (m Model) handleGitHubRefreshed(msg githubRefreshed) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	if msg.err == nil && !m.isCurrentTargetPR(msg.pr) {
		msg.err = gh.ErrPRNotFound
	}
	m.refreshing = false
	selectedKey := m.selectedConversationKey()
	now := time.Now().UTC().Format(time.RFC3339)
	var diffCmd tea.Cmd
	switch {
	case msg.err == nil:
		m.resetDetailCaches()
		m.cache.PR = &msg.pr
		if strings.TrimSpace(msg.pr.Title) != "" {
			m.title = msg.pr.Title
		}
		m.localAvailable = false
		m.cache.FetchedAt = now
		m.navigator.PRs = upsertPR(m.navigator.PRs, msg.pr)
		if matchesListState(msg.pr, closedPRListState) {
			m.prView, m.prListState, m.listRefreshing = closedPRsView, closedPRListState, false
			m.prListGeneration++
			m.activePRPage = prPageKey(m.prView, m.prListState, "")
		}
		m.applyPRFilters(msg.pr.Number)
		if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
			m.status = "PR list cache: " + err.Error()
		}
		diffCmd = m.useBase(msg.pr.BaseRefName, &msg.pr, msg.pr.URL)
		m.mergeReadiness, m.mergeReadinessErr = applyGitHubConflictFallback(m.mergeReadiness, m.mergeReadinessErr, msg.pr)
		stale := []string{}
		if msg.commentsErr == nil {
			m.cache.Comments = msg.comments
		} else {
			stale = append(stale, "comments")
		}
		if msg.activitiesErr == nil {
			m.cache.Activities = msg.activities
		} else {
			stale = append(stale, "activity")
		}
		m.cache.Reviews, m.cache.ReviewComments = msg.reviews, msg.reviewComments
		m.githubStatus = "GitHub: updated now"
		if len(stale) > 0 {
			m.githubStatus = "GitHub: PR updated · " + strings.Join(stale, "/") + " stale"
		}
	case errors.Is(msg.err, gh.ErrPRNotFound):
		m.localAvailable = m.currentBranch != "HEAD" && m.currentBranch != m.defaultBranch
		m.cache.PR = nil
		m.cache.Comments = nil
		m.cache.Activities = nil
		m.cache.Reviews = nil
		m.cache.ReviewComments = nil
		m.cache.FetchedAt = now
		m.githubStatus = "Local only · no GitHub PR"
		m.applyPRFilters(0)
	default:
		m.githubStatus = "Offline · showing cached GitHub data"
	}
	m.reloadLocalConversation()
	m.invalidateConversation()
	if err := gh.SaveCache(m.cachePath, m.cache); err != nil {
		m.status = "GitHub cache: " + err.Error()
	} else if strings.HasPrefix(m.status, "GitHub cache") {
		m.status = ""
	}
	m.layout()
	m.restoreConversationSelection(selectedKey)
	return m, tea.Batch(diffCmd, m.sync(), m.nextCIPoll(), loadRichContent(m.targetGeneration, m.list.Width-7, m.cache.PR, m.cache.Comments, m.cache.Activities))

}

// ciPollTargetsCurrentPR reports whether a CI poll message still belongs to
// the PR shown on the detail screen.
func (m Model) ciPollTargetsCurrentPR(generation uint64) bool {
	return generation == m.targetGeneration && m.screen == detailScreen && m.cache.PR != nil
}
