package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	bspinner "charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

func isShiftTab(msg tea.KeyPressMsg) bool {
	return msg.String() == "shift+tab"
}

func reservedReviewKey(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyPressMsg)
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
		outboxFlushed,
		prStatusDone, prActionDone, ciPolled, ciPollTick, diffRendered,
		richBodiesLoaded, avatarColorsLoaded, listAvatarColorsLoaded,
		localLoaded, checkoutReloaded, rawDetailLoaded, baseResolved,
		browserDone, tea.WindowSizeMsg, bspinner.TickMsg:
		return true
	}
	return false
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A modal popup owns the keyboard only; async completions fall through to
	// the main switch below so background work keeps landing.
	if m.overlay != nil && !asyncCompletion(msg) {
		if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
			return m.overlay.handleKey(m, keyMsg)
		}
		// The editor overlay also needs non-key messages (cursor blink).
		if handler, ok := m.overlay.(overlayMsgHandler); ok {
			return handler.handleMsg(m, msg)
		}
		return m, nil
	}
	if m.diffTerminal != nil && m.diffTerminal.Handles(msg) && !reservedReviewKey(msg) {
		cmd := m.diffTerminal.Update(msg)
		if !m.diffTerminal.Available() {
			if err := m.diffTerminal.Err(); err != nil {
				m.status = err.Error() + " · showing raw diff"
			}
			if m.detailView.focus == focusReview {
				m.detailView.focus = focusConversation
			}
			m.detailView.reviewWide = false
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
		m.help.SetWidth(msg.Width)
		if m.overlayHostsEditor() {
			m.sizeLocalEditor() // keep the open editor overlay fitting the new size
		}
		m.layout()
		if m.screen == detailScreen {
			// Mermaid diagrams are rendered at the pane width; re-render them
			// for the new size.
			return m, tea.Batch(m.sync(), m.dispatchRichContent())
		}
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
	case checkoutReloaded:
		next, cmd := m.handleCheckoutReloaded(msg)
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
	case outboxFlushed:
		next, cmd := m.handleOutboxFlushed(msg)
		return next, cmd
	case prStatusDone:
		next, cmd := m.handlePRStatusDone(msg)
		return next, cmd
	case remoteLoaded:
		next, cmd := m.handleRemoteLoaded(msg)
		return next, cmd
	case localLoaded:
		next, cmd := m.handleLocalLoaded(msg)
		return next, cmd
	case navigatorCacheSaved:
		if msg.err != nil {
			m.status = "PR list cache: " + msg.err.Error()
		}
		return m, nil
	case cacheSaved:
		if msg.err != nil {
			m.status = "GitHub cache: " + msg.err.Error()
		} else if strings.HasPrefix(m.status, "GitHub cache") {
			m.status = ""
		}
		return m, nil
	case baseResolved:
		next, cmd := m.handleBaseResolved(msg)
		return next, cmd
	case ciPollTick:
		if m.ciPollTargetsCurrentPR(msg.generation) && !m.refreshing && m.cache.PR.Number == msg.number && pollableCI(*m.cache.PR) {
			return m, pollCI(m.client, msg.generation, msg.number)
		}
		return m, nil
	case ciPolled:
		next, cmd := m.handleCIPolled(msg)
		return next, cmd
	case githubRefreshed:
		next, cmd := m.handleGitHubRefreshed(msg)
		return next, cmd
	case richBodiesLoaded:
		// The key hashes width + bodies, so a match proves the result fits the
		// current content even if the generation moved on: nothing resets
		// lastRichContentKey, so discarding here would leave dispatchRichContent
		// returning nil forever and mermaid diagrams missing until a resize.
		if msg.key != richContentKey(m.list.Width()-7, m.cache.PR, m.cache.Comments, m.cache.Activities) {
			return m, nil
		}
		m.detailView.richBodies = msg.bodies
		m.detailView.invalidateConversation()
		m.layout()
		return m, m.sync()
	case avatarColorsLoaded:
		// Key-only check for the same reason as richBodiesLoaded above.
		if msg.key != richContentKey(m.list.Width()-7, m.cache.PR, m.cache.Comments, m.cache.Activities) {
			return m, nil
		}
		if m.avatarColors == nil {
			m.avatarColors = map[string]string{}
		}
		for login, color := range msg.colors {
			m.avatarColors[login] = color
		}
		m.detailView.invalidateConversation()
		m.layout()
		return m, m.sync()
	case listAvatarColorsLoaded:
		if msg.generation != m.prList.generation {
			return m, nil
		}
		if m.avatarColors == nil {
			m.avatarColors = map[string]string{}
		}
		for login, color := range msg.colors {
			m.avatarColors[login] = color
		}
		clear(m.prList.rowCache)
		return m, m.sync()
	case tea.MouseMsg:
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			// The review pane sits after the bordered left pane; +1 row for its own top border.
			if local, ok := translateDiffMouse(msg, m.list.Width()+paneChromeW, m.detail.Width(), m.detail.Height(), m.headerHeight()+1); ok {
				m.detailView.focus = focusReview
				return m, m.diffTerminal.Update(local)
			}
			if _, click := msg.(tea.MouseClickMsg); click && m.detailView.focus == focusReview {
				m.detailView.focus = focusConversation
			}
		}
		return m, nil

	case tea.PasteMsg:
		// v1 delivered pastes as key messages; v2 splits them out, so route
		// them to whichever input the equivalent key would have reached.
		if m.screen == prListScreen && m.prList.filterEditing {
			m.prList.filterQuery += msg.Content
			return m, nil
		}
		if m.detailView.focus == focusReview && m.diffTerminal != nil && m.diffTerminal.Available() {
			return m, m.diffTerminal.Update(msg)
		}
		return m, nil

	case tea.KeyPressMsg:
		// A notice reports the last action. Acting again makes it stale, and
		// leaving it up hides the status of whatever runs next — including
		// whether a refresh is running at all.
		m.notice = ""
		next, cmd := m.handleKey(msg)
		return next, cmd
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.Update(msg)
	return m, cmd
}
