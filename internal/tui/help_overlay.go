package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// helpOverlay lists the keys that work on the screen it was opened from. It
// captures them at open time: while a popup is up, contextualKeys only
// reports the popup's own confirm/cancel.
type helpOverlay struct {
	keys keyMap
}

func (m Model) openHelp() (Model, tea.Cmd) {
	m.overlay = helpOverlay{keys: m.contextualKeys()}
	return m, nil
}

func (o helpOverlay) handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "?", "esc", "q", "enter":
		m.overlay = nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// ponytail: no scrolling; a terminal shorter than the longest column clips
// the popup. Add a viewport if the key list outgrows small terminals.
func (o helpOverlay) render(Model) string {
	titles := []string{"Navigate", "Act"}
	var columns []string
	for i, group := range o.keys.helpGroups() {
		keyWidth := 0
		var enabled []key.Binding
		for _, binding := range group {
			if binding.Enabled() {
				enabled = append(enabled, binding)
				keyWidth = max(keyWidth, lipgloss.Width(binding.Help().Key))
			}
		}
		if len(enabled) == 0 {
			continue
		}
		rows := []string{stAccent.Bold(true).Render(titles[i]), ""}
		for _, binding := range enabled {
			help := binding.Help()
			pad := strings.Repeat(" ", keyWidth-lipgloss.Width(help.Key))
			rows = append(rows, stFg.Bold(true).Render(help.Key)+pad+"  "+stMuted.Render(help.Desc))
		}
		columns = append(columns, strings.Join(rows, "\n"))
	}
	for i := 0; i < len(columns)-1; i++ {
		columns[i] = lipgloss.NewStyle().PaddingRight(4).Render(columns[i])
	}
	legend := stMuted.Render("Merge: ") + stGreenF.Render("⇄") + stMuted.Render(" mergeable · ") +
		stAttention.Render("⊘") + stMuted.Render(" blocked · ") + stRedF.Render("⚠") +
		stMuted.Render(" conflicts · ? unknown")
	body := strings.Join([]string{
		stBold.Render("Keyboard shortcuts"),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top, columns...),
		"",
		legend,
		"",
		stMuted.Render("? / Esc close"),
	}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAccent)).
		Padding(1, 2).
		Render(body)
}
