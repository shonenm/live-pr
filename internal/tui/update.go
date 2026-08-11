package tui

import (
	"github.com/charmbracelet/bubbles/key"
	bspinner "github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func reservedReviewKey(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	return ok && (key.Matches(keyMsg, keys.FocusLeft) || key.Matches(keyMsg, keys.Focus))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.diffTerminal != nil && m.diffTerminal.Handles(msg) && !reservedReviewKey(msg) {
		cmd := m.diffTerminal.Update(msg)
		if !m.diffTerminal.Available() {
			if err := m.diffTerminal.Err(); err != nil {
				m.status = err.Error() + " · showing raw diff"
			}
			m.focusDiff = false
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
	case prActionDone:
		next, cmd := m.handlePRActionDone(msg)
		return next, cmd
	case browserDone:
		next, cmd := m.handleBrowserDone(msg)
		return next, cmd
	case publishDone:
		next, cmd := m.handlePublishDone(msg)
		return next, cmd
	case remoteLoaded:
		next, cmd := m.handleRemoteLoaded(msg)
		return next, cmd
	case githubRefreshed:
		next, cmd := m.handleGitHubRefreshed(msg)
		return next, cmd
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
