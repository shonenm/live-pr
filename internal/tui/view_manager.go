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

// viewManagerOverlay edits a draft copy of the configured PR list views. The
// draft is applied and written to disk on save, so Esc always leaves the
// running configuration untouched.
type viewManagerOverlay struct {
	draft      []config.View
	cursor     int
	editField  viewEditField
	editIndex  int
	nameInput  textinput.Model
	queryInput textinput.Model
	errText    string
}

// openViewManager starts editing a draft copy of the configured views.
func (m Model) openViewManager() (Model, tea.Cmd) {
	o := viewManagerOverlay{
		draft:     append([]config.View(nil), m.views...),
		cursor:    int(m.prView),
		editIndex: -1,
	}
	if o.cursor >= len(o.draft) {
		o.cursor = 0
	}
	m.overlay = o
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

// startEdit opens the two-field form. index < 0 appends a new view.
func (o viewManagerOverlay) startEdit(m Model, index int) (Model, tea.Cmd) {
	width := max(20, min(60, m.w-24))
	name, query := "", ""
	if index >= 0 && index < len(o.draft) {
		name, query = o.draft[index].Name, o.draft[index].Query
	}
	o.editIndex = index
	o.nameInput = newViewInput(name, "Review requested", width)
	o.queryInput = newViewInput(query, "review-requested:@me", width)
	o.editField, o.errText = viewEditName, ""
	cmd := o.nameInput.Focus()
	m.overlay = o
	return m, cmd
}

func (o viewManagerOverlay) handleKey(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	if o.editField != viewEditNone {
		return o.handleEditKey(m, msg)
	}
	switch msg.String() {
	case "esc", "q":
		m.overlay = nil
		return m, nil
	case "j", "down":
		if len(o.draft) > 0 {
			o.cursor = (o.cursor + 1) % len(o.draft)
		}
	case "k", "up":
		if len(o.draft) > 0 {
			o.cursor = (o.cursor + len(o.draft) - 1) % len(o.draft)
		}
	case "J":
		o = o.move(1)
	case "K":
		o = o.move(-1)
	case "n", "a":
		return o.startEdit(m, -1)
	case "enter", "e":
		if len(o.draft) > 0 {
			return o.startEdit(m, o.cursor)
		}
	case "d":
		if len(o.draft) > 0 {
			o.draft = append(o.draft[:o.cursor], o.draft[o.cursor+1:]...)
			if o.cursor >= len(o.draft) {
				o.cursor = max(0, len(o.draft)-1)
			}
			o.errText = ""
		}
	case "ctrl+s":
		return o.save(m)
	case "ctrl+c":
		return m, tea.Quit
	}
	m.overlay = o
	return m, nil
}

// move reorders the selected view, keeping the cursor on it.
func (o viewManagerOverlay) move(delta int) viewManagerOverlay {
	target := o.cursor + delta
	if o.cursor < 0 || o.cursor >= len(o.draft) || target < 0 || target >= len(o.draft) {
		return o
	}
	draft := append([]config.View(nil), o.draft...)
	draft[o.cursor], draft[target] = draft[target], draft[o.cursor]
	o.draft, o.cursor, o.errText = draft, target, ""
	return o
}

func (o viewManagerOverlay) handleEditKey(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		o.editField, o.editIndex, o.errText = viewEditNone, -1, ""
		m.overlay = o
		return m, nil
	case "tab", "shift+tab", "down", "up":
		var cmd tea.Cmd
		if o.editField == viewEditName {
			o.editField = viewEditQuery
			o.nameInput.Blur()
			cmd = o.queryInput.Focus()
		} else {
			o.editField = viewEditName
			o.queryInput.Blur()
			cmd = o.nameInput.Focus()
		}
		m.overlay = o
		return m, cmd
	case "enter", "ctrl+s":
		return o.commitEdit(m)
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	if o.editField == viewEditName {
		o.nameInput, cmd = o.nameInput.Update(msg)
	} else {
		o.queryInput, cmd = o.queryInput.Update(msg)
	}
	m.overlay = o
	return m, cmd
}

func (o viewManagerOverlay) commitEdit(m Model) (Model, tea.Cmd) {
	edited := config.View{
		Name:  strings.TrimSpace(o.nameInput.Value()),
		Query: strings.TrimSpace(o.queryInput.Value()),
	}
	if edited.Name == "" {
		o.errText = "name must not be empty"
		m.overlay = o
		return m, nil
	}
	for i, view := range o.draft {
		if i != o.editIndex && strings.EqualFold(view.Name, edited.Name) {
			o.errText = "another view already uses that name"
			m.overlay = o
			return m, nil
		}
	}
	draft := append([]config.View(nil), o.draft...)
	if o.editIndex >= 0 && o.editIndex < len(draft) {
		draft[o.editIndex] = edited
		o.cursor = o.editIndex
	} else {
		draft = append(draft, edited)
		o.cursor = len(draft) - 1
	}
	o.draft = draft
	o.editField, o.editIndex, o.errText = viewEditNone, -1, ""
	o.nameInput.Blur()
	o.queryInput.Blur()
	m.overlay = o
	return m, nil
}

// save writes the draft to the config file and reloads the tabs.
func (o viewManagerOverlay) save(m Model) (Model, tea.Cmd) {
	draft := config.NormalizeViews(o.draft)
	path := config.ViewsPath(m.root)
	if err := config.SaveViews(path, draft); err != nil {
		o.errText = err.Error()
		m.overlay = o
		return m, nil
	}
	selected := m.selectedPRNumber()
	previous := m.viewName(m.prView)
	m.views = draft
	m.overlay = nil
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

func (o viewManagerOverlay) render(m Model) string {
	width := max(40, min(84, m.w-10))
	lines := []string{stBold.Render("Views"), stMuted.Render("saved to " + config.ViewsPath(m.root)), ""}
	if len(o.draft) == 0 {
		lines = append(lines, stMuted.Render("(no views — n adds one; saving an empty list restores the defaults)"))
	}
	for i, view := range o.draft {
		prefix, nameStyle := "  ", stFg
		if i == o.cursor {
			prefix, nameStyle = "▸ ", stAccent.Bold(true)
		}
		query := view.Query
		if query == "" {
			query = "(all open)"
		}
		row := prefix + nameStyle.Render(view.Name) + stMuted.Render("  "+query)
		lines = append(lines, ansi.Truncate(row, width-6, "…"))
	}
	if o.editField != viewEditNone {
		title := "Edit view"
		if o.editIndex < 0 {
			title = "New view"
		}
		lines = append(lines, "", stBold.Render(title))
		lines = append(lines, o.formRow("name ", viewEditName, o.nameInput.View()))
		lines = append(lines, o.formRow("query", viewEditQuery, o.queryInput.View()))
	}
	if o.errText != "" {
		lines = append(lines, "", stRedF.Render(o.errText))
	}
	lines = append(lines, "", stMuted.Render(o.hint()))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAccent)).
		Padding(1, 2).
		Width(width).
		Render(strings.Join(lines, "\n"))
}

func (o viewManagerOverlay) formRow(label string, field viewEditField, input string) string {
	marker, style := "  ", stMuted
	if o.editField == field {
		marker, style = "▸ ", stAccent
	}
	return marker + style.Render(label) + " " + input
}

func (o viewManagerOverlay) hint() string {
	if o.editField != viewEditNone {
		return "Tab switch field · Enter apply · Esc back"
	}
	return fmt.Sprintf("j/k move · J/K reorder · e edit · n new · d delete · Ctrl+S save (%d) · Esc discard", len(o.draft))
}
