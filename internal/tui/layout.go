// Pane size computation for both screens.
package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
)

const (
	footerLines     = 1
	paneChromeW     = 4 // border + one space padding per side
	paneChromeH     = 2 // top and bottom border
	dividerW        = 3 // padded rule between the file list and the diff
	listRatio       = 52
	reviewListRatio = 38
	prListPaneRatio = 45
)

func (m *Model) layout() {
	if m.w <= 0 || m.h <= 0 {
		return
	}
	if m.screen == prListScreen {
		bodyH := max(3, m.h-m.headerHeight()-footerLines-paneChromeH)
		ratio := m.diffSplitRatio
		if ratio <= 0 {
			ratio = prListPaneRatio
		}
		listPaneW := max(4, m.w*ratio/100)
		minPaneW := max(14, m.diffMinPaneWidth)
		if minPaneW <= 0 {
			minPaneW = 24
		}
		if listPaneW < minPaneW {
			listPaneW = minPaneW
		}
		if m.w-listPaneW < minPaneW {
			listPaneW = max(minPaneW, m.w-minPaneW)
		}
		listW := max(4, listPaneW-paneChromeW)
		detailW := max(4, m.w-listPaneW-paneChromeW)
		if !m.ready {
			m.list = viewport.New(listW, bodyH)
			m.detail = viewport.New(detailW, bodyH)
			m.ready = true
		} else {
			if m.list.Width != listW {
				clear(m.prRowCache)
			}
			m.list.Width, m.list.Height = listW, bodyH
			m.detail.Width, m.detail.Height = detailW, bodyH
		}
		return
	}
	ratio := m.diffSplitRatio
	if ratio <= 0 {
		ratio = listRatio
	}
	minPaneW := max(14, m.diffMinPaneWidth)
	if minPaneW <= 0 {
		minPaneW = 24
	}
	if minPaneW > m.w/2 {
		minPaneW = max(8, m.w/2)
	}
	var leftPaneW, rightPaneW int
	conversationWide := m.reviewWide && !m.focusDiff && !m.focusExplorer
	if conversationWide {
		leftPaneW, rightPaneW = m.w, 0
	} else if m.reviewWide {
		leftPaneW, rightPaneW = 0, m.w
	} else {
		leftPaneW = max(minPaneW, m.w*ratio/100)
		rightPaneW = m.w - leftPaneW
		if rightPaneW < minPaneW {
			rightPaneW = minPaneW
			leftPaneW = max(minPaneW, m.w-minPaneW)
		}
	}
	listW, rightW := 0, max(4, rightPaneW-paneChromeW)
	if leftPaneW > 0 {
		listW = max(4, leftPaneW-paneChromeW)
	}
	bodyH := m.h - m.headerHeight() - footerLines - paneChromeH
	if bodyH < 3 {
		bodyH = 3
	}
	// The file list and the diff share one frame, split by an inner rule, so
	// the review side reads as a single region instead of two boxes.
	explorerW, detailW := 1, rightW
	if m.fileExplorerMode() {
		explorerW = max(14, rightW/3)
		if rightW-explorerW-dividerW < 20 {
			explorerW = max(8, rightW-dividerW-20)
		}
		detailW = max(4, rightW-explorerW-dividerW)
	}
	if !m.ready {
		m.list = viewport.New(listW, max(1, bodyH-1))
		m.explorer = viewport.New(explorerW, bodyH)
		m.detail = viewport.New(detailW, bodyH)
		m.ready = true
	} else {
		m.list.Width = listW
		m.list.Height = bodyH
		if m.active == conversationTab {
			m.list.Height = max(1, bodyH-1)
		}
		m.explorer.Width, m.explorer.Height = explorerW, bodyH
		m.detail.Width, m.detail.Height = detailW, bodyH
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Resize(detailW, bodyH)
	}
}
