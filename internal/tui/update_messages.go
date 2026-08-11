package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func (m Model) handlePRListRefreshed(msg prListRefreshed) (Model, tea.Cmd) {
	if msg.generation != m.prListGeneration || msg.state != m.prListState {
		return m, nil
	}
	m.listRefreshing = false
	if msg.err != nil {
		m.githubStatus = "Offline · showing cached PR list"
		return m, m.sync()
	}
	selectedNumber := m.selectedPRNumber()
	m.prPreviewLoading = map[int]bool{}
	m.prPreviewLoaded = map[int]bool{}
	m.viewerLogin = msg.viewer
	m.navigator.ViewerLogin = msg.viewer
	m.navigator.PRs = replacePRsForState(m.navigator.PRs, msg.prs, msg.state)
	m.navigator.PRsState = msg.state.String()
	if m.navigator.FetchedStates == nil {
		m.navigator.FetchedStates = map[string]bool{}
	}
	m.navigator.FetchedStates[msg.state.String()] = true
	if m.screen == detailScreen && !m.remote && m.cache.PR == nil {
		for i := range msg.prs {
			if isCurrentPR(msg.prs[i], m.currentBranch) {
				m.cache.PR = &msg.prs[i]
				m.localAvailable = false
				break
			}
		}
	}
	m.applyPRFilters(selectedNumber)
	m.navigator.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
		m.status = "PR list cache: " + err.Error()
	}
	m.restorePRSelection(selectedNumber)
	m.githubStatus = "GitHub: PR list updated"
	if m.screen == prListScreen && m.autoOpenCurrent {
		for i := range m.openPRs {
			if m.openPRs[i].Number > 0 && m.isCurrentTargetPR(m.openPRs[i]) {
				m.autoOpenCurrent = false
				st := store.ForBranch(m.root, m.currentBranch)
				cache, _ := gh.LoadCache(st.GitHubCache(), m.currentBranch)
				if err := m.loadLocal(st, cache, &m.openPRs[i]); err != nil {
					m.status = err.Error()
					break
				}
				var cmds []tea.Cmd
				cmds = append(cmds, fetchGitHub(m.currentBranch, m.currentPRNumber(), m.targetGeneration), m.sync())
				if m.diffTerminal != nil {
					cmds = append(cmds, m.diffTerminal.Init())
				}
				return m, tea.Batch(cmds...)
			}
		}
	}
	return m, m.sync()

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
	if err := m.loadLocal(st, cache, &msg.pr); err != nil {
		m.status = err.Error()
		return m, nil
	}
	cmds := []tea.Cmd{fetchGitHub(m.currentBranch, m.currentPRNumber(), m.targetGeneration), m.sync()}
	if m.diffTerminal != nil {
		cmds = append(cmds, m.diffTerminal.Init())
	}
	return m, tea.Batch(cmds...)

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
	m.applyPRFilters(selectedNumber)
	if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
		m.status = "PR list cache: " + err.Error()
	}
	if m.selectedPRNumber() == msg.number {
		m.status = ""
	}
	return m, m.sync()

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
		m.listRefreshing = true
		m.prListGeneration++
		m.githubStatus = fmt.Sprintf("GitHub: refreshing %s pull requests…", m.prListState.Label())
		return m, tea.Batch(fetchPRList(m.prListGeneration, m.prListState), m.startSpinner())
	}
	m.close()
	next, err := New()
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

func (m Model) handleRemoteLoaded(msg remoteLoaded) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	m.refreshing = false
	selectedKey := m.selectedConversationKey()
	now := time.Now().UTC().Format(time.RFC3339)
	m.resetDetailCaches()
	m.cache.PR = &msg.pr
	if msg.commentsErr == nil {
		m.cache.Comments = msg.comments
	}
	if msg.activitiesErr == nil {
		m.cache.Activities = msg.activities
	}
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
	m.base = git.ResolveBase(msg.pr.BaseRefName)
	m.diffBase = remoteReviewBase(msg.pr)
	m.reviewRange = m.diffBase + "..." + m.headRev
	m.commits, _ = git.CommitsRange(m.diffBase, m.headRev)
	m.files, _ = git.ChangedFilesRange(m.diffBase, m.headRev)
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
	m.githubStatus = "GitHub: selected PR updated"
	if len(stale) > 0 {
		m.githubStatus += " · " + strings.Join(stale, "/") + " stale"
	}
	m.diffTerminal = embeddedterm.New(m.diffCommand, m.root, embeddedterm.Environment(m.reviewRange, m.diffBase, m.head, m.headRev, msg.pr.URL, ""))
	m.layout()
	m.restoreConversationSelection(selectedKey)
	cmds := []tea.Cmd{m.sync()}
	if m.diffTerminal != nil {
		cmds = append(cmds, m.diffTerminal.Init())
	}
	return m, tea.Batch(cmds...)

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
		m.localAvailable = false
		m.cache.FetchedAt = now
		m.navigator.PRs = upsertPR(m.navigator.PRs, msg.pr)
		if matchesListState(msg.pr, closedPRListState) {
			m.prView, m.prListState, m.listRefreshing = closedPRsView, closedPRListState, false
		}
		m.applyPRFilters(msg.pr.Number)
		if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
			m.status = "PR list cache: " + err.Error()
		}
		diffCmd = m.useBase(msg.pr.BaseRefName, &msg.pr, msg.pr.URL)
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
		m.githubStatus = "GitHub: updated now"
		if len(stale) > 0 {
			m.githubStatus = "GitHub: PR updated · " + strings.Join(stale, "/") + " stale"
		}
	case errors.Is(msg.err, gh.ErrPRNotFound):
		m.localAvailable = m.currentBranch != "HEAD" && m.currentBranch != m.defaultBranch
		m.cache.PR = nil
		m.cache.Comments = nil
		m.cache.Activities = nil
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
	return m, tea.Batch(diffCmd, m.sync())

}
