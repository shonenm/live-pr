package tui

import (
	"github.com/charmbracelet/bubbles/key"
	bspinner "github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func isShiftTab(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyShiftTab || msg.String() == "shift+tab" || msg.String() == "backtab"
}

func reservedReviewKey(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	return ok && (key.Matches(keyMsg, keys.FocusLeft) || key.Matches(keyMsg, keys.Focus) || isShiftTab(keyMsg))
}

// asyncCompletion reports messages that must reach their handlers even while a
// modal popup owns the keyboard. Dropping them used to leave in-flight state
// stuck: reviewSubmitting had no recovery path until restart, refreshing and
// remoteCommentBusy stalled, and the CI polling chain died.
func asyncCompletion(msg tea.Msg) bool {
	switch msg.(type) {
	case prListRefreshed, currentBranchPRLoaded, prPreviewLoaded, remoteLoaded,
		githubRefreshed, publishDone, reviewSubmitted, remoteCommentDone,
		prStatusDone, prActionDone, ciPolled, ciPollTick, diffRendered,
		richBodiesLoaded, avatarColorsLoaded, listAvatarColorsLoaded,
		browserDone, tea.WindowSizeMsg, bspinner.TickMsg:
		return true
	}
	return false
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Modal popups own the keyboard only; async completions fall through to
	// the main switch below so background work keeps landing.
	modal := m.statusPR.Number > 0 || m.reviewSubmitEvent != "" ||
		m.localEditMode != noLocalEdit || m.localDeleteTarget != "" || m.remoteDeleteID > 0
	if modal && !asyncCompletion(msg) {
		switch {
		case m.statusPR.Number > 0:
			if keyMsg, ok := msg.(tea.KeyMsg); ok {
				return m.handlePRStatusKey(keyMsg)
			}
			return m, nil
		case m.reviewSubmitEvent != "":
			if keyMsg, ok := msg.(tea.KeyMsg); ok {
				return m.handleReviewSubmitKey(keyMsg)
			}
			return m, nil
		default:
			// The editor overlay also needs non-key messages (cursor blink).
			return m.handleLocalOverlay(msg)
		}
	}
	if m.diffTerminal != nil && m.diffTerminal.Handles(msg) && !reservedReviewKey(msg) {
		cmd := m.diffTerminal.Update(msg)
		if !m.diffTerminal.Available() {
			if err := m.diffTerminal.Err(); err != nil {
				m.status = err.Error() + " · showing raw diff"
			}
			m.focusDiff, m.reviewWide = false, false
			m.layout()
			return m, tea.Batch(cmd, m.sync())
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case bspinner.TickMsg:
		var cmd tea.Cmd
		m.loadSpinner, cmd = m.loadSpinner.Update(msg)
		if !m.isLoading() {
			m.spinnerRunning = false
			return m, nil
		}
		return m, cmd

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.Width = msg.Width
		if m.localEditMode != noLocalEdit {
			m.sizeLocalEditor() // keep the open editor overlay fitting the new size
		}
		m.layout()
		return m, m.sync()

	case prListRefreshed:
		next, cmd := m.handlePRListRefreshed(msg)
		return next, cmd
	case currentBranchPRLoaded:
		next, cmd := m.handleCurrentBranchPRLoaded(msg)
		return next, cmd
	case prPreviewLoaded:
		next, cmd := m.handlePRPreviewLoaded(msg)
		return next, cmd
	case diffRendered:
		next, cmd := m.handleDiffRendered(msg)
		return next, cmd
	case rawDetailLoaded:
		next, cmd := m.handleRawDetailLoaded(msg)
		return next, cmd
	case prActionDone:
		next, cmd := m.handlePRActionDone(msg)
		return next, cmd
	case browserDone:
		next, cmd := m.handleBrowserDone(msg)
		return next, cmd
	case publishDone:
		next, cmd := m.handlePublishDone(msg)
		return next, cmd
	case reviewSubmitted:
		next, cmd := m.handleReviewSubmitted(msg)
		return next, cmd
	case remoteCommentDone:
		next, cmd := m.handleRemoteCommentDone(msg)
		return next, cmd
	case prStatusDone:
		next, cmd := m.handlePRStatusDone(msg)
		return next, cmd
	case remoteLoaded:
		next, cmd := m.handleRemoteLoaded(msg)
		return next, cmd
	case ciPollTick:
		if m.ciPollTargetsCurrentPR(msg.generation) && !m.refreshing && m.cache.PR.Number == msg.number && prCIHealth(*m.cache.PR) == "pending" {
			return m, pollCI(msg.generation, msg.number)
		}
		return m, nil
	case ciPolled:
		next, cmd := m.handleCIPolled(msg)
		return next, cmd
	case githubRefreshed:
		next, cmd := m.handleGitHubRefreshed(msg)
		return next, cmd
	case richBodiesLoaded:
		if msg.generation != m.targetGeneration || msg.key != richContentKey(m.cache.PR, m.cache.Comments, m.cache.Activities) {
			return m, nil
		}
		m.richBodies = msg.bodies
		m.invalidateConversation()
		m.layout()
		return m, m.sync()
	case avatarColorsLoaded:
		if msg.generation != m.targetGeneration || msg.key != richContentKey(m.cache.PR, m.cache.Comments, m.cache.Activities) {
			return m, nil
		}
		if m.avatarColors == nil {
			m.avatarColors = map[string]string{}
		}
		for login, color := range msg.colors {
			m.avatarColors[login] = color
		}
		m.invalidateConversation()
		m.layout()
		return m, m.sync()
	case listAvatarColorsLoaded:
		if msg.generation != m.prListGeneration {
			return m, nil
		}
		if m.avatarColors == nil {
			m.avatarColors = map[string]string{}
		}
		for login, color := range msg.colors {
			m.avatarColors[login] = color
		}
		clear(m.prRowCache)
		return m, m.sync()
	case tea.MouseMsg:
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			// The review pane sits after the bordered left pane; +1 row for its own top border.
			if local, ok := translateDiffMouse(msg, m.list.Width+paneChromeW, m.detail.Width, m.detail.Height, m.headerHeight()+1); ok {
				m.focusDiff = true
				return m, m.diffTerminal.Update(local)
			}
			if msg.Action == tea.MouseActionPress {
				m.focusDiff = false
			}
		}
		return m, nil

	case tea.KeyMsg:
		next, cmd := m.handleKey(msg)
		return next, cmd
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.Update(msg)
	return m, cmd
}
