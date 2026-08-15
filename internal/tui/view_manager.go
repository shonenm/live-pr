package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
)

// viewEditField identifies which line of the edit form has focus.
type viewEditField uint8

const (
	viewEditNone viewEditField = iota
	viewEditName
	viewEditQuery
)

// openViewManager starts editing a draft copy of the configured views. The
// draft is applied and written to disk on save, so Esc always leaves the
// running configuration untouched.
func (m Model) openViewManager() (Model, tea.Cmd) {
	m.viewManager = true
	m.viewDraft = append([]config.View(nil), m.views...)
	m.viewCursor = int(m.prView)
	if m.viewCursor >= len(m.viewDraft) {
		m.viewCursor = 0
	}
	m.viewEditField, m.viewEditIndex, m.viewManagerError = viewEditNone, -1, ""
	m.notice, m.status = "", ""
	return m, nil
}

func newViewInput(value, placeholder string, width int) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.CharLimit = 512
	input.SetValue(value)
	input.Width = width
	return input
}

// startViewEdit opens the two-field form. index < 0 appends a new view.
func (m Model) startViewEdit(index int) (Model, tea.Cmd) {
	width := max(20, min(60, m.w-24))
	name, query := "", ""
	if index >= 0 && index < len(m.viewDraft) {
		name, query = m.viewDraft[index].Name, m.viewDraft[index].Query
	}
	m.viewEditIndex = index
	m.viewNameInput = newViewInput(name, "Review requested", width)
	m.viewQueryInput = newViewInput(query, "review-requested:@me", width)
	m.viewEditField, m.viewManagerError = viewEditName, ""
	return m, m.viewNameInput.Focus()
}

func (m Model) handleViewManagerKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.viewEditField != viewEditNone {
		return m.handleViewEditKey(msg)
	}
	switch msg.String() {
	case "esc", "q":
		m.viewManager = false
		m.viewDraft, m.viewManagerError = nil, ""
		return m, nil
	case "j", "down":
		if len(m.viewDraft) > 0 {
			m.viewCursor = (m.viewCursor + 1) % len(m.viewDraft)
		}
	case "k", "up":
		if len(m.viewDraft) > 0 {
			m.viewCursor = (m.viewCursor + len(m.viewDraft) - 1) % len(m.viewDraft)
		}
	case "J":
		m = m.moveViewDraft(1)
	case "K":
		m = m.moveViewDraft(-1)
	case "n", "a":
		return m.startViewEdit(-1)
	case "enter", "e":
		if len(m.viewDraft) > 0 {
			return m.startViewEdit(m.viewCursor)
		}
	case "d":
		if len(m.viewDraft) > 0 {
			m.viewDraft = append(m.viewDraft[:m.viewCursor], m.viewDraft[m.viewCursor+1:]...)
			if m.viewCursor >= len(m.viewDraft) {
				m.viewCursor = max(0, len(m.viewDraft)-1)
			}
			m.viewManagerError = ""
		}
	case "ctrl+s":
		return m.saveViewManager()
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// moveViewDraft reorders the selected view, keeping the cursor on it.
func (m Model) moveViewDraft(delta int) Model {
	target := m.viewCursor + delta
	if m.viewCursor < 0 || m.viewCursor >= len(m.viewDraft) || target < 0 || target >= len(m.viewDraft) {
		return m
	}
	draft := append([]config.View(nil), m.viewDraft...)
	draft[m.viewCursor], draft[target] = draft[target], draft[m.viewCursor]
	m.viewDraft, m.viewCursor, m.viewManagerError = draft, target, ""
	return m
}

func (m Model) handleViewEditKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.viewEditField, m.viewEditIndex, m.viewManagerError = viewEditNone, -1, ""
		return m, nil
	case "tab", "shift+tab", "down", "up":
		if m.viewEditField == viewEditName {
			m.viewEditField = viewEditQuery
			m.viewNameInput.Blur()
			return m, m.viewQueryInput.Focus()
		}
		m.viewEditField = viewEditName
		m.viewQueryInput.Blur()
		return m, m.viewNameInput.Focus()
	case "enter", "ctrl+s":
		return m.commitViewEdit()
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	if m.viewEditField == viewEditName {
		m.viewNameInput, cmd = m.viewNameInput.Update(msg)
	} else {
		m.viewQueryInput, cmd = m.viewQueryInput.Update(msg)
	}
	return m, cmd
}

func (m Model) commitViewEdit() (Model, tea.Cmd) {
	edited := config.View{
		Name:  strings.TrimSpace(m.viewNameInput.Value()),
		Query: strings.TrimSpace(m.viewQueryInput.Value()),
	}
	if edited.Name == "" {
		m.viewManagerError = "name must not be empty"
		return m, nil
	}
	for i, view := range m.viewDraft {
		if i != m.viewEditIndex && strings.EqualFold(view.Name, edited.Name) {
			m.viewManagerError = "another view already uses that name"
			return m, nil
		}
	}
	draft := append([]config.View(nil), m.viewDraft...)
	if m.viewEditIndex >= 0 && m.viewEditIndex < len(draft) {
		draft[m.viewEditIndex] = edited
		m.viewCursor = m.viewEditIndex
	} else {
		draft = append(draft, edited)
		m.viewCursor = len(draft) - 1
	}
	m.viewDraft = draft
	m.viewEditField, m.viewEditIndex, m.viewManagerError = viewEditNone, -1, ""
	m.viewNameInput.Blur()
	m.viewQueryInput.Blur()
	return m, nil
}

// saveViewManager writes the draft to the config file and reloads the tabs.
func (m Model) saveViewManager() (Model, tea.Cmd) {
	draft := config.NormalizeViews(m.viewDraft)
	path := config.ViewsPath(m.root)
	if err := config.SaveViews(path, draft); err != nil {
		m.viewManagerError = err.Error()
		return m, nil
	}
	selected := m.selectedPRNumber()
	previous := m.viewName(m.prView)
	m.views = draft
	m.viewManager, m.viewDraft, m.viewManagerError = false, nil, ""
	// Stay on the same tab by name; a removed tab falls back to the first.
	m.prView = 0
	for i, view := range m.views {
		if strings.EqualFold(view.Name, previous) {
			m.prView = prView(i)
			break
		}
	}
	m.viewCountsValid = false
	m.prPages = map[string]prPageState{}
	m.notice = "Views saved to " + path
	return m, m.applyPRViewState(selected)
}

func (m Model) renderViewManagerPopup() string {
	width := max(40, min(84, m.w-10))
	lines := []string{stBold.Render("Views"), stMuted.Render("saved to " + config.ViewsPath(m.root)), ""}
	if len(m.viewDraft) == 0 {
		lines = append(lines, stMuted.Render("(no views — n adds one; saving an empty list restores the defaults)"))
	}
	for i, view := range m.viewDraft {
		prefix, nameStyle := "  ", stFg
		if i == m.viewCursor {
			prefix, nameStyle = "▸ ", stAccent.Bold(true)
		}
		query := view.Query
		if query == "" {
			query = "(all open)"
		}
		row := prefix + nameStyle.Render(view.Name) + stMuted.Render("  "+query)
		lines = append(lines, ansi.Truncate(row, width-6, "…"))
	}
	if m.viewEditField != viewEditNone {
		title := "Edit view"
		if m.viewEditIndex < 0 {
			title = "New view"
		}
		lines = append(lines, "", stBold.Render(title))
		lines = append(lines, m.viewFormRow("name ", viewEditName, m.viewNameInput.View()))
		lines = append(lines, m.viewFormRow("query", viewEditQuery, m.viewQueryInput.View()))
	}
	if m.viewManagerError != "" {
		lines = append(lines, "", stRedF.Render(m.viewManagerError))
	}
	lines = append(lines, "", stMuted.Render(m.viewManagerHint()))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAccent)).
		Padding(1, 2).
		Width(width).
		Render(strings.Join(lines, "\n"))
}

func (m Model) viewFormRow(label string, field viewEditField, input string) string {
	marker, style := "  ", stMuted
	if m.viewEditField == field {
		marker, style = "▸ ", stAccent
	}
	return marker + style.Render(label) + " " + input
}

func (m Model) viewManagerHint() string {
	if m.viewEditField != viewEditNone {
		return "Tab switch field · Enter apply · Esc back"
	}
	return fmt.Sprintf("j/k move · J/K reorder · e edit · n new · d delete · Ctrl+S save (%d) · Esc discard", len(m.viewDraft))
}
