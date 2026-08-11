package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/store"
)

func (m *Model) sizeLocalEditor() {
	m.localEditor.SetWidth(max(24, min(82, m.w-16)))
	m.localEditor.SetHeight(max(6, min(18, m.h-12)))
}

func (m Model) openLocalEditor(mode localEditMode, value, target string) (Model, tea.Cmd) {
	editor := textarea.New()
	editor.Prompt = ""
	editor.ShowLineNumbers = false
	editor.FocusedStyle.CursorLine = lipgloss.NewStyle()
	editor.BlurredStyle.CursorLine = lipgloss.NewStyle()
	editor.CharLimit = 65536
	editor.SetValue(value)
	m.localEditor = editor
	m.localEditMode, m.localEditTarget, m.localEditError = mode, target, ""
	m.sizeLocalEditor()
	return m, m.localEditor.Focus()
}

func (m Model) startLocalComment() (Model, tea.Cmd) {
	if m.remote || m.active != conversationTab || m.focusDiff || m.focusExplorer {
		return m, nil
	}
	return m.openLocalEditor(addLocalComment, "kind: decision\n\n", "")
}

func (m Model) editSelectedLocalItem() (Model, tea.Cmd) {
	if m.remote || m.active != conversationTab || m.focusDiff || m.focusExplorer {
		return m, nil
	}
	item := m.selectedConversationItem()
	if item == nil {
		return m, nil
	}
	if item.summary != nil {
		return m.openLocalEditor(editLocalSummary, *item.summary, "")
	}
	if item.event == nil || item.event.Kind == event.Commit {
		m.status = "only local summary and comments can be edited"
		return m, nil
	}
	return m.openLocalEditor(editLocalComment, formatLocalComment(*item.event), item.event.ID)
}

func (m Model) deleteSelectedLocalComment() (Model, tea.Cmd) {
	if m.remote || m.active != conversationTab || m.focusDiff || m.focusExplorer {
		return m, nil
	}
	item := m.selectedConversationItem()
	if item == nil || item.event == nil || item.event.Kind == event.Commit {
		m.status = "select a local comment to delete"
		return m, nil
	}
	m.localDeleteTarget, m.localDeleteTitle = item.event.ID, item.event.Title
	m.status = ""
	return m, nil
}

func formatLocalComment(ev event.Event) string {
	body := fmt.Sprintf("kind: %s\n\n%s", ev.Kind, ev.Title)
	if strings.TrimSpace(ev.Body) != "" {
		body += "\n\n" + strings.TrimSpace(ev.Body)
	}
	return body
}

func parseLocalComment(value string, allowSummary bool) (event.Kind, string, string, error) {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "kind:") {
		return "", "", "", fmt.Errorf("first line must be kind: decision, pivot, or note")
	}
	kind := event.Kind(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "kind:")))
	valid := kind == event.Decision || kind == event.Pivot || kind == event.Note || (allowSummary && kind == event.Summary)
	if !valid {
		return "", "", "", fmt.Errorf("kind must be decision, pivot, or note")
	}
	lines = lines[1:]
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", "", fmt.Errorf("comment title must not be empty")
	}
	title := strings.TrimSpace(lines[0])
	body := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	return kind, title, body, nil
}

func (m Model) saveLocalEdit() (Model, tea.Cmd) {
	st := store.ForBranch(m.root, m.currentBranch)
	selectedKey := "local-summary"
	switch m.localEditMode {
	case editLocalSummary:
		if strings.TrimSpace(m.localEditor.Value()) == "" {
			m.localEditError = "summary must not be empty"
			return m, nil
		}
		if err := st.WriteConclusion(m.localEditor.Value()); err != nil {
			m.localEditError = err.Error()
			return m, nil
		}
	case addLocalComment:
		kind, title, body, err := parseLocalComment(m.localEditor.Value(), false)
		if err != nil {
			m.localEditError = err.Error()
			return m, nil
		}
		comment := event.New(kind, title, body)
		comment.Author = "user"
		created, err := event.Create(st.Timeline(), comment)
		if err != nil {
			m.localEditError = err.Error()
			return m, nil
		}
		selectedKey = "event:" + created.ID + ":0"
	case editLocalComment:
		kind, title, body, err := parseLocalComment(m.localEditor.Value(), true)
		if err != nil {
			m.localEditError = err.Error()
			return m, nil
		}
		current, ok := localEventByID(m.events, m.localEditTarget)
		if !ok {
			m.localEditError = "comment no longer exists"
			return m, nil
		}
		current.Kind, current.Title, current.Body, current.Author = kind, title, body, "user"
		if _, err := event.Update(st.Timeline(), current.ID, current); err != nil {
			m.localEditError = err.Error()
			return m, nil
		}
		selectedKey = "event:" + current.ID + ":0"
	}
	m.localEditor.Blur()
	m.localEditMode, m.localEditTarget, m.localEditError = noLocalEdit, "", ""
	m.reloadLocalConversation()
	m.restoreConversationSelection(selectedKey)
	m.notice = "Local PR updated"
	return m, m.sync()
}

func localEventByID(events []event.Event, id string) (event.Event, bool) {
	for _, ev := range events {
		if ev.ID == id {
			return ev, true
		}
	}
	return event.Event{}, false
}

func (m Model) handleLocalOverlay(msg tea.Msg) (Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.w, m.h = size.Width, size.Height
		m.sizeLocalEditor()
		m.layout()
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if m.localDeleteTarget != "" {
		if !ok {
			return m, nil
		}
		switch key.String() {
		case "y":
			if err := event.Delete(m.timelinePath, m.localDeleteTarget); err != nil {
				m.status = err.Error()
			} else {
				m.reloadLocalConversation()
				m.notice = "Comment deleted"
			}
			m.localDeleteTarget, m.localDeleteTitle = "", ""
			return m, m.sync()
		case "n", "esc", "q":
			m.localDeleteTarget, m.localDeleteTitle = "", ""
		}
		return m, nil
	}
	if m.localEditMode == noLocalEdit {
		return m, nil
	}
	if ok {
		switch key.String() {
		case "ctrl+s":
			return m.saveLocalEdit()
		case "esc":
			m.localEditor.Blur()
			m.localEditMode, m.localEditTarget, m.localEditError = noLocalEdit, "", ""
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.localEditor, cmd = m.localEditor.Update(msg)
	return m, cmd
}

func (m Model) renderLocalEditorPopup() string {
	title, hint := "Add local comment", "first line: kind: decision | pivot | note"
	if m.localEditMode == editLocalComment {
		title = "Edit local comment"
	}
	if m.localEditMode == editLocalSummary {
		title, hint = "Edit final summary", "follow the repository PR template; describe the final result"
	}
	lines := []string{stBold.Render(title), stMuted.Render(hint), "", m.localEditor.View()}
	if m.localEditError != "" {
		lines = append(lines, "", stRedF.Render(m.localEditError))
	}
	lines = append(lines, "", stMuted.Render("Ctrl+S save · Esc cancel"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAccent)).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
}

func (m Model) renderLocalDeletePopup() string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAttention)).
		Padding(1, 3).
		Width(max(24, min(60, m.w-14))).
		Render(stBold.Render("Delete local comment?") + "\n\n" + stFg.Render(m.localDeleteTitle) + "\n\n" + stMuted.Render("y confirm · n / Esc cancel"))
}
