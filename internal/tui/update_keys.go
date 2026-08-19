package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/store"
)

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.screen == prListScreen {
		return m.handlePRListKey(msg)
	}
	return m.handleDetailKey(msg)
}

func (m Model) handlePRListKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.prList.filterEditing {
		selected := m.prList.selectedPRNumber()
		switch msg.String() {
		case "enter":
			m.prList.filterEditing, m.prList.filterBeforeEdit, m.prList.filterSelectionBeforeEdit = false, "", 0
			return m, m.applyPRViewState(selected)
		case "esc":
			selected = m.prList.filterSelectionBeforeEdit
			m.prList.filterQuery, m.prList.filterEditing, m.prList.filterBeforeEdit, m.prList.filterSelectionBeforeEdit = m.prList.filterBeforeEdit, false, "", 0
			m.prList.restorePRSelection(selected)
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "backspace":
			runes := []rune(m.prList.filterQuery)
			if len(runes) > 0 {
				m.prList.filterQuery = string(runes[:len(runes)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.prList.filterQuery += string(msg.Runes)
			} else if msg.String() == " " {
				m.prList.filterQuery += " "
			}
		}
		return m, nil
	}
	if next, cmd, handled := m.handlePRActionConfirmKey(msg); handled {
		return next, cmd
	}
	if key.Matches(msg, m.keys.PreviewUp) {
		scrollQuarter(&m.detail, false)
		return m, nil
	}
	if key.Matches(msg, m.keys.PreviewDown) {
		scrollQuarter(&m.detail, true)
		return m, nil
	}
	if handled, cmd := m.handleVimNavigation(msg); handled {
		return m, cmd
	}
	switch {
	case key.Matches(msg, m.keys.Filter):
		m.prList.filterBeforeEdit, m.prList.filterSelectionBeforeEdit, m.prList.filterEditing = m.prList.filterQuery, m.prList.selectedPRNumber(), true
		return m, nil
	case msg.String() == "esc" && m.prList.filterQuery != "":
		selected := m.prList.selectedPRNumber()
		m.prList.filterQuery = ""
		return m, m.applyPRViewState(selected)
	case key.Matches(msg, m.keys.ManageViews):
		return m.openViewManager()
	case key.Matches(msg, m.keys.PrevView):
		selected := m.prList.selectedPRNumber()
		m.prList.view = m.stepView(-1)
		return m, m.applyPRViewState(selected)
	case key.Matches(msg, m.keys.NextView):
		selected := m.prList.selectedPRNumber()
		m.prList.view = m.stepView(1)
		return m, m.applyPRViewState(selected)
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	case key.Matches(msg, m.keys.Status):
		return m.openPRStatus(m.prList.selectedPR())
	case key.Matches(msg, m.keys.Merge):
		if m.prList.state == openPRListState {
			if pr := m.prList.selectedPR(); pr != nil && pr.Number > 0 && pr.HeadRefOID != "" {
				m.pendingPRAction, m.prActionNumber, m.prActionPR = mergePR, pr.Number, *pr
				m.mergeMethodCursor = 0
				m.status, m.notice = "", ""
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.CopyURL):
		return m, m.copySelectedURL()
	case key.Matches(msg, m.keys.Checkout):
		if pr := m.prList.selectedPR(); pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) {
			m.pendingPRAction, m.prActionNumber, m.prActionPR = checkoutPR, pr.Number, *pr
			m.status, m.notice = "", ""
		}
		return m, nil
	case key.Matches(msg, m.keys.Close):
		if m.prList.state == openPRListState {
			if pr := m.prList.selectedPR(); pr != nil && pr.Number > 0 {
				m.pendingPRAction, m.prActionNumber, m.prActionPR = closePR, pr.Number, *pr
				m.status, m.notice = "", ""
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.ToggleStack):
		if stack, ok := m.prList.stackForPR(m.prList.selectedPRNumber()); ok {
			collapsing := !m.prList.collapsedStacks[stack.ID]
			m.prList.collapsedStacks[stack.ID] = collapsing
			selected := m.prList.selectedPRNumber()
			if collapsing {
				selected = stack.Entries[0].PR.Number
			}
			m.applyPRFilters(selected)
			return m, m.sync()
		}
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		if m.prList.refreshing {
			// Say so rather than swallowing the key: silence reads as a
			// broken refresh.
			m.githubStatus = "GitHub: fetching " + m.viewName(m.prList.view) + " pull requests…"
			return m, m.startSpinner()
		}
		m.prList.generation++
		if m.prList.pages == nil {
			m.prList.pages = map[string]prPageState{}
		}
		page := m.prList.pages[m.prList.activePage]
		page.fresh, page.loading = false, false
		m.prList.pages[m.prList.activePage] = page
		// Re-check the branch's own PR too: once it leaves the open view,
		// nothing else would ever tell the list that it closed.
		return m, tea.Batch(m.requestPRPage(true), fetchCurrentBranchPRState(m.client, m.currentBranch))
	case key.Matches(msg, m.keys.Down):
		return m, m.moveCursorBy(1)
	case key.Matches(msg, m.keys.Up):
		return m, m.moveCursorBy(-1)
	case key.Matches(msg, m.keys.Select):
		pr := m.prList.selectedPR()
		if pr == nil {
			return m, nil
		}
		// Remember the tab so b returns here rather than guessing.
		m.detailOrigin, m.detailOriginSet = m.prList.view, true
		if !m.isCurrentTargetPR(*pr) {
			return m, m.openRemote(*pr)
		}
		st := store.ForBranch(m.root, m.currentBranch)
		cache, _ := st.LoadGitHubCache()
		return m, tea.Batch(m.startLocalLoad(st, cache, pr), m.startSpinner())
	}
	return m, nil
}

// handlePRActionConfirmKey drives the y/n confirmation for a pending PR
// action and swallows keys while one runs; handled is false when neither
// state applies. Both screens share this state machine.
func (m Model) handlePRActionConfirmKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.pendingPRAction == mergePR {
		switch msg.String() {
		case "up", "k":
			m.mergeMethodCursor = (m.mergeMethodCursor + len(mergeMethodOptions) - 1) % len(mergeMethodOptions)
			return m, nil, true
		case "down", "j":
			m.mergeMethodCursor = (m.mergeMethodCursor + 1) % len(mergeMethodOptions)
			return m, nil, true
		case "m":
			return m.submitMerge(gh.MergeCommit)
		case "s":
			return m.submitMerge(gh.MergeSquash)
		case "r":
			return m.submitMerge(gh.MergeRebase)
		case "y", "enter":
			return m.submitMerge(m.selectedMergeMethod())
		case "n", "q", "esc":
			m.pendingPRAction, m.prActionNumber, m.prActionPR = noPRAction, 0, gh.PR{}
			return m, nil, true
		case "ctrl+c":
			return m, tea.Quit, true
		default:
			return m, nil, true
		}
	}
	if m.pendingPRAction != noPRAction {
		switch msg.String() {
		case "y":
			action, pr := m.pendingPRAction, m.prActionPR
			m.pendingPRAction = noPRAction
			m.prActionRunning = action
			m.notice = ""
			return m, tea.Batch(runPRAction(m.client, action, pr, gh.MergeCommit), m.startSpinner()), true
		case "n", "q", "esc":
			m.pendingPRAction, m.prActionNumber, m.prActionPR = noPRAction, 0, gh.PR{}
			return m, nil, true
		case "ctrl+c":
			return m, tea.Quit, true
		default:
			return m, nil, true
		}
	}
	if m.prActionRunning != noPRAction {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit, true
		}
		return m, nil, true
	}
	return m, nil, false
}

// submitMerge fires the pending merge with the chosen method.
func (m Model) submitMerge(method gh.MergeMethod) (Model, tea.Cmd, bool) {
	pr := m.prActionPR
	m.pendingPRAction = noPRAction
	m.prActionRunning = mergePR
	m.notice = ""
	return m, tea.Batch(runPRAction(m.client, mergePR, pr, method), m.startSpinner()), true
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (Model, tea.Cmd) {

	if next, cmd, handled := m.handlePRActionConfirmKey(msg); handled {
		return next, cmd
	}
	if key.Matches(msg, m.keys.PRList) {
		selected := m.currentPRNumber()
		m.prList.view = m.listViewForReturn(selected)
		m.detailOriginSet = false
		m.targetGeneration++
		if m.diffTerminal != nil {
			m.diffTerminal.Close()
		}
		m.diffTerminal = nil
		m.detailView.focus = focusConversation
		m.screen = prListScreen
		m.autoOpenCurrent = false
		m.refreshing, m.publishing = false, false
		m.detailView.active = conversationTab
		m.status = ""
		m.layout()
		return m, m.applyPRViewState(selected)
	}
	// Tab cycles focus: conversation → review → conversation.
	if key.Matches(msg, m.keys.Focus) {
		if m.detailView.focus == focusReview {
			m.detailView.focus, m.detailView.reviewWide = focusConversation, false
		} else {
			m.detailView.focus = focusReview
		}
		m.layout()
		return m, m.sync()
	}
	// Shift-Tab toggles the focused pane to full width. From conversation it
	// expands conversation; from review/explorer it expands the review side.
	if isShiftTab(msg) {
		m.detailView.reviewWide = !m.detailView.reviewWide
		// Expanding from the conversation keeps it focused (full-width
		// conversation hides the review); otherwise the review takes over.
		if m.detailView.reviewWide && m.detailView.focus != focusConversation {
			m.detailView.focus = focusReview
		}
		m.layout()
		return m, m.sync()
	}
	if m.fileExplorerMode() && key.Matches(msg, m.keys.FocusRight) {
		m.detailView.focus = focusExplorer
		return m, nil
	}
	if m.detailView.focus != focusReview && key.Matches(msg, m.keys.FocusRight) {
		m.detailView.focus = focusReview
		return m, nil
	}
	// Comment/review/edit keys only apply when the conversation pane is focused;
	// when the embedded reviewer (nvim) has focus, forward them to the terminal.
	if m.detailView.focus == focusConversation {
		if key.Matches(msg, m.keys.AddComment) {
			return m.startAddComment()
		}
		if key.Matches(msg, m.keys.InlineReview) {
			return m.startInlineReviewComment()
		}
		if key.Matches(msg, m.keys.Review) {
			return m.openReviewSubmit()
		}
		if key.Matches(msg, m.keys.EditLocal) {
			return m.editSelectedLocalItem()
		}
		if key.Matches(msg, m.keys.DeleteLocal) {
			return m.deleteSelectedLocalComment()
		}
	}
	if m.detailView.focus == focusReview {
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			return m, m.diffTerminal.Update(msg)
		}
		if m.fileExplorerMode() && key.Matches(msg, m.keys.Commits) {
			return m, m.toggleFileCheck()
		}
		if msg.String() == "g" {
			if m.pendingG {
				m.pendingG = false
				m.detail.GotoTop()
			} else {
				m.pendingG = true
			}
			return m, nil
		}
		m.pendingG = false
		if key.Matches(msg, m.keys.Bottom) {
			m.detail.GotoBottom()
			return m, nil
		}
		if key.Matches(msg, m.keys.PreviewUp) {
			scrollQuarter(&m.detail, false)
			return m, nil
		}
		if key.Matches(msg, m.keys.PreviewDown) {
			scrollQuarter(&m.detail, true)
			return m, nil
		}
		switch msg.String() {
		case "k":
			m.detail.ScrollUp(1)
			return m, nil
		case "j":
			m.detail.ScrollDown(1)
			return m, nil
		}
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}
	if m.detailView.focus == focusExplorer {
		if m.fileExplorerMode() {
			if key.Matches(msg, m.keys.PreviewUp) {
				scrollQuarter(&m.detail, false)
				return m, nil
			}
			if key.Matches(msg, m.keys.PreviewDown) {
				scrollQuarter(&m.detail, true)
				return m, nil
			}
		}
		if handled, cmd := m.handleVimNavigation(msg); handled {
			return m, cmd
		}
	}
	if m.screen == detailScreen && m.detailView.focus == focusConversation {
		if key.Matches(msg, m.keys.PreviewUp) {
			scrollQuarter(&m.list, false)
			return m, nil
		}
		if key.Matches(msg, m.keys.PreviewDown) {
			scrollQuarter(&m.list, true)
			return m, nil
		}
	}
	if handled, cmd := m.handleVimNavigation(msg); handled {
		return m, cmd
	}
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		if m.refreshing || m.publishing {
			m.githubStatus = "GitHub: refreshing…"
			return m, m.startSpinner()
		}
		m.refreshing = true
		m.githubStatus = "GitHub: refreshing…"
		if m.remote && m.cache.PR != nil {
			m.targetGeneration++
			return m, tea.Batch(fetchRemotePR(m.client, *m.cache.PR, m.targetGeneration, m.cachedDetail()), m.startSpinner())
		}
		return m, tea.Batch(fetchGitHub(m.client, m.detailView.head, m.currentPRNumber(), m.targetGeneration, m.cachedDetail()), m.startSpinner())
	case key.Matches(msg, m.keys.Status):
		return m.openPRStatus(m.cache.PR)
	case key.Matches(msg, m.keys.Merge):
		if m.canMergeCurrentPR() {
			m.pendingPRAction, m.prActionNumber, m.prActionPR = mergePR, m.cache.PR.Number, *m.cache.PR
			m.mergeMethodCursor = 0
			m.status, m.notice = "", ""
		}
		return m, nil
	case key.Matches(msg, m.keys.Publish):
		if m.publishing {
			return m, nil
		}
		if m.refreshing {
			m.status = "wait for GitHub refresh before publishing"
			return m, nil
		}
		m.publishing = true
		m.status = "publishing PR…"
		base := m.detailView.base
		generation := m.targetGeneration
		return m, tea.Batch(func() tea.Msg {
			result, err := publish.Run(publish.Options{Base: base})
			return publishDone{generation: generation, result: result, err: err}
		}, m.startSpinner())
	case key.Matches(msg, m.keys.CopyURL):
		return m, m.copySelectedURL()
	case key.Matches(msg, m.keys.Browse):
		url := m.selectedBrowseURL()
		if url == "" {
			return m, nil
		}
		return m, func() tea.Msg {
			if err := browserCommand(url).Run(); err == nil {
				return browserDone{}
			}
			if clipErr := copyToClipboard(url); clipErr == nil {
				return browserDone{copied: true}
			}
			return browserDone{err: errors.New("cannot open browser or copy URL")}
		}
	case msg.String() == "C" && key.Matches(msg, m.keys.Checkout):
		// Detail keeps c for the commits tab, so checkout answers to C only.
		if pr := m.cache.PR; pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) {
			m.pendingPRAction, m.prActionNumber, m.prActionPR = checkoutPR, pr.Number, *pr
			m.status, m.notice = "", ""
		}
		return m, nil
	case key.Matches(msg, m.keys.Commits):
		if m.fileExplorerMode() && m.detailView.focus == focusExplorer {
			return m, m.toggleFileCheck()
		}
		m.detailView.active = commitsTab
		m.status = "select a commit and press Enter"
		m.layout()
		return m, m.sync()
	case key.Matches(msg, m.keys.Conflicts):
		m.detailView.active, m.status = conflictsTab, ""
		m.layout()
		return m, m.sync()
	case key.Matches(msg, m.keys.Checks):
		m.detailView.active, m.status = checksTab, ""
		m.layout()
		return m, m.sync()
	case key.Matches(msg, m.keys.Back):
		if m.detailView.active == conversationTab {
			return m, nil
		}
		wasCommitReview := m.detailView.active == commitsTab && m.detailView.reviewSHA != ""
		m.detailView.active, m.status = conversationTab, ""
		m.layout()
		if !wasCommitReview {
			return m, m.sync()
		}
		cmd := m.restartReview("", m.prURL())
		return m, tea.Batch(cmd, m.sync())
	case key.Matches(msg, m.keys.Select):
		if m.detailView.active != commitsTab {
			return m, nil
		}
		sha := m.detailView.selectedCommitSHA()
		if sha == "" {
			return m, nil
		}
		m.status = ""
		cmd := m.restartReview(sha, m.prURL())
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			m.detailView.focus = focusReview
		} else if m.detailView.focus == focusReview {
			m.detailView.focus = focusConversation
		}
		return m, tea.Batch(cmd, m.sync())
	case key.Matches(msg, m.keys.Down):
		if m.detailView.focus == focusExplorer {
			if m.detailView.fileCursor < len(m.detailView.files)-1 {
				m.detailView.fileCursor++
				return m, m.sync()
			}
			return m, nil
		}
		if m.detailView.cursors[m.detailView.active] < m.activeLen()-1 {
			m.detailView.cursors[m.detailView.active]++
			return m, m.sync()
		}
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if m.detailView.focus == focusExplorer {
			if m.detailView.fileCursor > 0 {
				m.detailView.fileCursor--
				return m, m.sync()
			}
			return m, nil
		}
		if m.detailView.cursors[m.detailView.active] > 0 {
			m.detailView.cursors[m.detailView.active]--
			return m, m.sync()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.Update(msg)
	return m, cmd
}

// copySelectedURL copies the selected pull request or comment URL. The
// clipboard write can block on a helper process, so it rides a Cmd and
// reports through the same message as browsing.
func (m Model) copySelectedURL() tea.Cmd {
	url := m.selectedBrowseURL()
	if url == "" {
		return nil
	}
	return func() tea.Msg {
		if err := copyToClipboard(url); err != nil {
			return browserDone{err: fmt.Errorf("copy URL: %w", err)}
		}
		return browserDone{copied: true}
	}
}
