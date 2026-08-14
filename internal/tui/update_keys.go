package tui

import (
	"errors"

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
	if m.filterEditing {
		selected := m.selectedPRNumber()
		switch msg.String() {
		case "enter":
			m.filterEditing, m.filterBeforeEdit, m.filterSelectionBeforeEdit = false, "", 0
			return m, m.applyPRViewState(selected)
		case "esc":
			selected = m.filterSelectionBeforeEdit
			m.filterQuery, m.filterEditing, m.filterBeforeEdit, m.filterSelectionBeforeEdit = m.filterBeforeEdit, false, "", 0
			m.restorePRSelection(selected)
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "backspace":
			runes := []rune(m.filterQuery)
			if len(runes) > 0 {
				m.filterQuery = string(runes[:len(runes)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.filterQuery += string(msg.Runes)
			} else if msg.String() == " " {
				m.filterQuery += " "
			}
		}
		return m, nil
	}
	if m.pendingPRAction != noPRAction {
		switch msg.String() {
		case "y":
			action, pr := m.pendingPRAction, m.prActionPR
			m.pendingPRAction = noPRAction
			m.prActionRunning = action
			m.notice = ""
			return m, tea.Batch(runPRAction(action, pr), m.startSpinner())
		case "n", "q", "esc":
			m.pendingPRAction, m.prActionNumber, m.prActionPR = noPRAction, 0, gh.PR{}
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		default:
			return m, nil
		}
	}
	if m.prActionRunning != noPRAction {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
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
	if handled, cmd := m.handleVimNavigation(msg); handled {
		return m, cmd
	}
	switch {
	case key.Matches(msg, m.keys.Filter):
		m.filterBeforeEdit, m.filterSelectionBeforeEdit, m.filterEditing = m.filterQuery, m.selectedPRNumber(), true
		return m, nil
	case msg.String() == "esc" && m.filterQuery != "":
		selected := m.selectedPRNumber()
		m.filterQuery = ""
		return m, m.applyPRViewState(selected)
	case key.Matches(msg, m.keys.PrevView):
		selected := m.selectedPRNumber()
		m.prView = (m.prView + prViewCount - 1) % prViewCount
		return m, m.applyPRViewState(selected)
	case key.Matches(msg, m.keys.NextView):
		selected := m.selectedPRNumber()
		m.prView = (m.prView + 1) % prViewCount
		return m, m.applyPRViewState(selected)
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Status):
		return m.openPRStatus(m.selectedPR())
	case key.Matches(msg, m.keys.Merge):
		if m.prListState == openPRListState {
			if pr := m.selectedPR(); pr != nil && pr.Number > 0 && pr.HeadRefOID != "" {
				m.pendingPRAction, m.prActionNumber, m.prActionPR = mergePR, pr.Number, *pr
				m.status, m.notice = "", ""
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.Checkout):
		if pr := m.selectedPR(); pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) {
			m.pendingPRAction, m.prActionNumber, m.prActionPR = checkoutPR, pr.Number, *pr
			m.status, m.notice = "", ""
		}
		return m, nil
	case key.Matches(msg, m.keys.Close):
		if m.prListState == openPRListState {
			if pr := m.selectedPR(); pr != nil && pr.Number > 0 {
				m.pendingPRAction, m.prActionNumber, m.prActionPR = closePR, pr.Number, *pr
				m.status, m.notice = "", ""
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.ToggleStack):
		if stack, ok := m.stackForPR(m.selectedPRNumber()); ok {
			collapsing := !m.collapsedStacks[stack.id]
			m.collapsedStacks[stack.id] = collapsing
			selected := m.selectedPRNumber()
			if collapsing {
				selected = stack.entries[0].pr.Number
			}
			m.applyPRFilters(selected)
			return m, m.sync()
		}
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		if m.listRefreshing {
			return m, nil
		}
		m.prListGeneration++
		if m.prPages == nil {
			m.prPages = map[string]prPageState{}
		}
		page := m.prPages[m.activePRPage]
		page.fresh, page.loading = false, false
		m.prPages[m.activePRPage] = page
		return m, m.requestPRPage(true)
	case key.Matches(msg, m.keys.Down):
		return m, m.moveCursorBy(1)
	case key.Matches(msg, m.keys.Up):
		return m, m.moveCursorBy(-1)
	case key.Matches(msg, m.keys.Select):
		pr := m.selectedPR()
		if pr == nil {
			return m, nil
		}
		if !m.isCurrentTargetPR(*pr) {
			return m, m.openRemote(*pr)
		}
		st := store.ForBranch(m.root, m.currentBranch)
		cache, _ := gh.LoadCache(st.GitHubCache(), m.currentBranch)
		if err := m.loadLocal(st, cache, pr); err != nil {
			m.status = err.Error()
			return m, nil
		}
		cmds := []tea.Cmd{fetchGitHub(m.currentBranch, m.currentPRNumber(), m.targetGeneration), m.sync(), m.startSpinner()}
		if m.diffTerminal != nil {
			cmds = append(cmds, m.diffTerminal.Init())
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (Model, tea.Cmd) {

	if m.pendingPRAction != noPRAction {
		switch msg.String() {
		case "y":
			action, pr := m.pendingPRAction, m.prActionPR
			m.pendingPRAction = noPRAction
			m.prActionRunning = action
			m.notice = ""
			return m, tea.Batch(runPRAction(action, pr), m.startSpinner())
		case "n", "q", "esc":
			m.pendingPRAction, m.prActionNumber, m.prActionPR = noPRAction, 0, gh.PR{}
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		default:
			return m, nil
		}
	}
	if m.prActionRunning != noPRAction {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.PRList) {
		selected := m.currentPRNumber()
		m.targetGeneration++
		if m.diffTerminal != nil {
			m.diffTerminal.Close()
		}
		m.diffTerminal = nil
		m.focusDiff, m.focusExplorer = false, false
		m.screen = prListScreen
		m.autoOpenCurrent = false
		m.refreshing, m.publishing = false, false
		m.active = conversationTab
		m.status = ""
		m.layout()
		return m, m.applyPRViewState(selected)
	}
	// Tab cycles focus: conversation → review (→ explorer if available) → conversation.
	if key.Matches(msg, m.keys.Focus) {
		if m.focusDiff {
			m.focusDiff, m.focusExplorer, m.reviewWide = false, false, false
		} else {
			m.focusDiff, m.focusExplorer = true, false
		}
		m.layout()
		return m, m.sync()
	}
	// Shift-Tab toggles the focused pane to full width. From conversation it
	// expands conversation; from review/explorer it expands the review side.
	if isShiftTab(msg) {
		m.reviewWide = !m.reviewWide
		if m.reviewWide && !m.focusDiff && !m.focusExplorer {
			m.focusDiff = false // conversation full-width: hide review
		} else if m.reviewWide {
			m.focusDiff, m.focusExplorer = true, false
		}
		m.layout()
		return m, m.sync()
	}
	if m.fileExplorerMode() && key.Matches(msg, m.keys.FocusRight) {
		if !m.focusExplorer {
			m.focusExplorer = true
		}
		return m, nil
	}
	if (m.focusDiff || m.focusExplorer) && key.Matches(msg, m.keys.FocusLeft) {
		m.focusDiff, m.focusExplorer, m.reviewWide = false, false, false
		m.layout()
		return m, m.sync()
	}
	if !m.focusDiff && key.Matches(msg, m.keys.FocusRight) {
		m.focusDiff = true
		return m, nil
	}
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
	if m.focusDiff {
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
	if m.focusExplorer {
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
	if m.screen == detailScreen && !m.focusDiff && !m.focusExplorer {
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
			return m, nil
		}
		m.refreshing = true
		m.githubStatus = "GitHub: refreshing…"
		if m.remote && m.cache.PR != nil {
			m.targetGeneration++
			return m, tea.Batch(fetchRemotePR(*m.cache.PR, m.targetGeneration), m.startSpinner())
		}
		return m, tea.Batch(fetchGitHub(m.head, m.currentPRNumber(), m.targetGeneration), m.startSpinner())
	case key.Matches(msg, m.keys.Status):
		return m.openPRStatus(m.cache.PR)
	case key.Matches(msg, m.keys.Merge):
		if m.canMergeCurrentPR() {
			m.pendingPRAction, m.prActionNumber, m.prActionPR = mergePR, m.cache.PR.Number, *m.cache.PR
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
		base := m.base
		generation := m.targetGeneration
		return m, tea.Batch(func() tea.Msg {
			result, err := publish.Run(publish.Options{Base: base})
			return publishDone{generation: generation, result: result, err: err}
		}, m.startSpinner())
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
	case key.Matches(msg, m.keys.Commits):
		if m.fileExplorerMode() && m.focusExplorer {
			return m, m.toggleFileCheck()
		}
		m.active = commitsTab
		m.status = "select a commit and press Enter"
		m.layout()
		return m, m.sync()
	case key.Matches(msg, m.keys.Conflicts):
		m.active, m.status = conflictsTab, ""
		m.layout()
		return m, m.sync()
	case key.Matches(msg, m.keys.Checks):
		m.active, m.status = checksTab, ""
		m.layout()
		return m, m.sync()
	case key.Matches(msg, m.keys.Back):
		if m.active == conversationTab {
			return m, nil
		}
		wasCommitReview := m.active == commitsTab && m.reviewSHA != ""
		m.active, m.status = conversationTab, ""
		m.layout()
		if !wasCommitReview {
			return m, m.sync()
		}
		cmd := m.restartReview("", m.prURL())
		return m, tea.Batch(cmd, m.sync())
	case key.Matches(msg, m.keys.Select):
		if m.active != commitsTab {
			return m, nil
		}
		sha := m.selectedCommitSHA()
		if sha == "" {
			return m, nil
		}
		m.status = ""
		cmd := m.restartReview(sha, m.prURL())
		m.focusDiff = m.diffTerminal != nil && m.diffTerminal.Available()
		return m, tea.Batch(cmd, m.sync())
	case key.Matches(msg, m.keys.Down):
		if m.focusExplorer {
			if m.fileCursor < len(m.files)-1 {
				m.fileCursor++
				return m, m.sync()
			}
			return m, nil
		}
		if m.cursors[m.active] < m.activeLen()-1 {
			m.cursors[m.active]++
			return m, m.sync()
		}
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if m.focusExplorer {
			if m.fileCursor > 0 {
				m.fileCursor--
				return m, m.sync()
			}
			return m, nil
		}
		if m.cursors[m.active] > 0 {
			m.cursors[m.active]--
			return m, m.sync()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.Update(msg)
	return m, cmd
}
