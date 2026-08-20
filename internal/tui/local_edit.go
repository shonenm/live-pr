package tui

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/event"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

// localEditOverlay hosts the shared localEditor textarea for local timeline
// edits and GitHub-bound comment or description edits. mode selects the save
// path; target carries the edited item's identity (a local event ID or
// "pr-description"); remoteCommentID names the GitHub comment being edited.
type localEditOverlay struct {
	mode            localEditMode
	target          string
	errText         string
	remoteCommentID int64
}

// localDeleteOverlay is the y/n confirm for deleting a local timeline comment.
type localDeleteOverlay struct {
	target string
	title  string
}

// remoteDeleteOverlay is the y/n confirm for deleting a GitHub comment.
type remoteDeleteOverlay struct {
	id    int64
	title string
}

// editorWidth is the widest the editor may be and still fit the popups that
// host it. The review popup declares min(80, w-14) and pads 2 on each side,
// so anything wider gets re-wrapped by the popup and shows breaks the text
// does not contain.
func (m Model) editorWidth() int {
	return max(24, min(80, m.w-14)-4)
}

func (m *Model) sizeLocalEditor() {
	m.localEditor.SetWidth(m.editorWidth())
	m.localEditor.SetHeight(max(6, min(18, m.h-12)))
}

// setupLocalEditor rebuilds the shared editor textarea with value and focuses
// it. Callers decide which overlay presents it: openLocalEditor for the edit
// overlay, the review submit popup for its message body.
func (m Model) setupLocalEditor(value string) (Model, tea.Cmd) {
	editor := textarea.New()
	editor.Prompt = ""
	editor.ShowLineNumbers = false
	styles := editor.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	editor.SetStyles(styles)
	editor.CharLimit = 65536
	// Terminals disagree on Shift+Enter: most send CR (already a newline
	// here), some send LF, which bubbletea reports as ctrl+j. Accept both,
	// plus Option/Alt+Enter, so the key inserts a newline either way.
	editor.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("enter", "ctrl+m", "ctrl+j", "alt+enter"),
		key.WithHelp("enter", "insert newline"),
	)
	editor.SetValue(value)
	m.localEditor = editor
	m.sizeLocalEditor()
	return m, m.localEditor.Focus()
}

func (m Model) openLocalEditor(o localEditOverlay, value string) (Model, tea.Cmd) {
	m, cmd := m.setupLocalEditor(value)
	m.overlay = o
	return m, cmd
}

func (m Model) startLocalComment() (Model, tea.Cmd) {
	if m.remote || m.detailView.active != conversationTab || m.detailView.focus != focusConversation {
		return m, nil
	}
	return m.openLocalEditor(localEditOverlay{mode: addLocalComment}, "kind: decision\n\n")
}

func (m Model) editSelectedLocalItem() (Model, tea.Cmd) {
	if m.detailView.active != conversationTab {
		m.status = "comments live in the Conversation tab (esc)"
		return m, nil
	}
	if m.detailView.focus != focusConversation {
		return m, nil
	}
	item := m.selectedConversationItem()
	if item == nil {
		return m, nil
	}
	switch item.kind() {
	case itemComment:
		// A GitHub conversation comment can be edited if the viewer authored
		// it. When viewerLogin is unknown (detail opened before the PR list
		// loaded it), allow the attempt — GitHub's API rejects others' edits.
		if m.viewerLogin != "" && !strings.EqualFold(item.comment.User.Login, m.viewerLogin) {
			m.status = "only your own GitHub comments can be edited"
			return m, nil
		}
		return m.openLocalEditor(localEditOverlay{mode: editRemoteComment, remoteCommentID: item.comment.ID}, item.comment.Body)
	case itemOutbox:
		m.status = "queued comments cannot be edited; discard with d and rewrite"
		return m, nil
	case itemReview, itemReviewComment:
		m.status = "review comments cannot be edited here; use GitHub"
		return m, nil
	case itemPRDescription:
		if m.cache.PR == nil || m.cache.PR.Number <= 0 {
			m.status = "no PR to edit"
			return m, nil
		}
		return m.openLocalEditor(localEditOverlay{mode: editRemoteComment, target: "pr-description"}, item.pr.Body)
	case itemActivity, itemPRCommit:
		m.status = "activity and CI events are not editable"
		return m, nil
	}
	if m.remote {
		return m, nil
	}
	switch item.kind() {
	case itemSummary:
		return m.openLocalEditor(localEditOverlay{mode: editLocalSummary}, *item.summary)
	case itemEvent:
		if item.event.Kind != event.Commit {
			return m.openLocalEditor(localEditOverlay{mode: editLocalComment, target: item.event.ID}, formatLocalComment(*item.event))
		}
	}
	m.status = "only local summary and comments can be edited"
	return m, nil
}

func (m Model) deleteSelectedLocalComment() (Model, tea.Cmd) {
	if m.detailView.active != conversationTab {
		m.status = "comments live in the Conversation tab (esc)"
		return m, nil
	}
	if m.detailView.focus != focusConversation {
		return m, nil
	}
	item := m.selectedConversationItem()
	if item == nil {
		m.status = "select a comment to delete"
		return m, nil
	}
	switch item.kind() {
	case itemComment:
		// GitHub issue comment: allow deleting your own.
		if m.viewerLogin != "" && !strings.EqualFold(item.comment.User.Login, m.viewerLogin) {
			m.status = "only your own GitHub comments can be deleted"
			return m, nil
		}
		// ansi.Truncate counts display cells, so multibyte text is never cut
		// mid-rune.
		m.overlay = remoteDeleteOverlay{id: item.comment.ID, title: ansi.Truncate(item.comment.Body, 60, "…")}
		m.status = ""
		return m, nil
	case itemOutbox:
		m.overlay = outboxDiscardOverlay{id: item.outbox.ID, title: ansi.Truncate(item.outbox.Body, 60, "…")}
		m.status = ""
		return m, nil
	case itemReview, itemReviewComment:
		m.status = "review comments cannot be deleted here; use GitHub"
		return m, nil
	case itemPRDescription, itemActivity, itemPRCommit:
		m.status = "this item cannot be deleted"
		return m, nil
	}
	if m.remote {
		return m, nil
	}
	if item.kind() != itemEvent || item.event.Kind == event.Commit {
		m.status = "select a local comment to delete"
		return m, nil
	}
	m.overlay = localDeleteOverlay{target: item.event.ID, title: item.event.Title}
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

func (o localEditOverlay) save(m Model) (Model, tea.Cmd) {
	// GitHub-bound targets are posted over the network, so they return early
	// with an async command; the local modes share the close-editor /
	// reload / notice tail below.
	if o.mode == editRemoteComment && o.target == "pr-description" {
		return o.savePRDescription(m)
	}
	if o.mode == addRemoteComment || o.mode == editRemoteComment {
		return o.saveRemoteComment(m)
	}
	st := store.ForBranch(m.root, m.currentBranch)
	selectedKey := "local-summary"
	var err error
	switch o.mode {
	case editLocalSummary:
		selectedKey, err = m.saveLocalSummary(st)
	case addLocalComment:
		selectedKey, err = m.saveNewLocalComment(st)
	case editReviewBody:
		selectedKey, err = m.saveReviewBodyDraft()
	case addInlineReviewComment:
		selectedKey, err = m.saveInlineReviewComment()
	case editLocalComment:
		selectedKey, err = m.saveEditedLocalComment(st, o.target)
	}
	if err != nil {
		o.errText = err.Error()
		m.overlay = o
		return m, nil
	}
	m.localEditor.Blur()
	m.overlay = nil
	if o.mode != editReviewBody && o.mode != addInlineReviewComment {
		m.reloadLocalConversation()
		m.notice = "Local PR updated"
	} else {
		m.notice = "Draft review updated"
	}
	m.restoreConversationSelection(selectedKey)
	return m, m.sync()
}

func (o localEditOverlay) savePRDescription(m Model) (Model, tea.Cmd) {
	body := m.localEditor.Value()
	if m.cache.PR == nil {
		o.errText = "no pull request"
		m.overlay = o
		return m, nil
	}
	number := m.cache.PR.Number
	m.localEditor.Blur()
	m.overlay = nil
	m.remoteCommentBusy = true
	m.status = "updating PR description…"
	return m, tea.Batch(updatePRDescription(m.client, number, body, m.targetGeneration), m.startSpinner())
}

func (o localEditOverlay) saveRemoteComment(m Model) (Model, tea.Cmd) {
	body := strings.TrimSpace(m.localEditor.Value())
	if body == "" {
		o.errText = "comment must not be empty"
		m.overlay = o
		return m, nil
	}
	if m.cache.PR == nil {
		o.errText = "no pull request for this comment"
		m.overlay = o
		return m, nil
	}
	editID := int64(0)
	if o.mode == editRemoteComment {
		editID = o.remoteCommentID
	}
	number := m.cache.PR.Number
	m.localEditor.Blur()
	m.overlay = nil
	m.remoteCommentBusy = true
	m.status = "sending comment…"
	return m, tea.Batch(postRemoteComment(m.client, number, body, editID, m.targetGeneration), m.startSpinner())
}

func (m *Model) saveLocalSummary(st *store.Store) (string, error) {
	if strings.TrimSpace(m.localEditor.Value()) == "" {
		return "", errors.New("summary must not be empty")
	}
	if err := st.WriteConclusion(m.localEditor.Value()); err != nil {
		return "", err
	}
	return "local-summary", nil
}

func (m *Model) saveNewLocalComment(st *store.Store) (string, error) {
	kind, title, body, err := parseLocalComment(m.localEditor.Value(), false)
	if err != nil {
		return "", err
	}
	comment := event.New(kind, title, body)
	comment.Author = "user"
	created, err := event.Create(st.Timeline(), comment)
	if err != nil {
		return "", err
	}
	return "event:" + created.ID + ":0", nil
}

func (m *Model) saveReviewBodyDraft() (string, error) {
	if err := m.loadReviewDraft(); err != nil {
		return "", err
	}
	m.reviewDraft.Body = strings.TrimSpace(m.localEditor.Value())
	if err := gh.SaveReviewDraft(m.reviewDraftPath, m.reviewDraft); err != nil {
		return "", err
	}
	return m.selectedConversationKey(), nil
}

func (m *Model) saveInlineReviewComment() (string, error) {
	comment, err := parseInlineReviewComment(m.localEditor.Value())
	if err != nil {
		return "", err
	}
	if err := m.loadReviewDraft(); err != nil {
		return "", err
	}
	m.reviewDraft.Comments = append(m.reviewDraft.Comments, comment)
	if err := gh.SaveReviewDraft(m.reviewDraftPath, m.reviewDraft); err != nil {
		return "", err
	}
	return m.selectedConversationKey(), nil
}

func (m *Model) saveEditedLocalComment(st *store.Store, target string) (string, error) {
	kind, title, body, err := parseLocalComment(m.localEditor.Value(), true)
	if err != nil {
		return "", err
	}
	current, ok := localEventByID(m.detailView.events, target)
	if !ok {
		return "", errors.New("comment no longer exists")
	}
	current.Kind, current.Title, current.Body, current.Author = kind, title, body, "user"
	if _, err := event.Update(st.Timeline(), current.ID, current); err != nil {
		return "", err
	}
	return "event:" + current.ID + ":0", nil
}

func localEventByID(events []event.Event, id string) (event.Event, bool) {
	for _, ev := range events {
		if ev.ID == id {
			return ev, true
		}
	}
	return event.Event{}, false
}

func (o localEditOverlay) handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	// Ctrl+S only: bubbletea cannot report ctrl+enter (the terminal sends
	// plain CR for it), and Enter has to stay a newline in the editor.
	case "ctrl+s":
		return o.save(m)
	case "esc":
		m.localEditor.Blur()
		m.overlay = nil
		return m, nil
	}
	var cmd tea.Cmd
	m.localEditor, cmd = m.localEditor.Update(msg)
	return m, cmd
}

// handleMsg feeds editor-internal messages such as cursor blink to the
// textarea. WindowSizeMsg is routed through the main Update switch (it
// resizes the editor there), so this handler never sees it.
func (o localEditOverlay) handleMsg(m Model, msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.localEditor, cmd = m.localEditor.Update(msg)
	return m, cmd
}

func (o localDeleteOverlay) handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if err := event.Delete(m.timelinePath, o.target); err != nil {
			m.status = err.Error()
		} else {
			m.reloadLocalConversation()
			m.notice = "Comment deleted"
		}
		m.overlay = nil
		return m, m.sync()
	case "n", "esc", "q":
		m.overlay = nil
	}
	return m, nil
}

func (o remoteDeleteOverlay) handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.overlay = nil
		m.remoteCommentBusy = true
		m.status = "deleting comment…"
		return m, tea.Batch(deleteRemoteComment(m.client, o.id, m.targetGeneration), m.startSpinner())
	case "n", "esc", "q":
		m.overlay = nil
	}
	return m, nil
}

func (o localEditOverlay) render(m Model) string {
	title, hint := "Add local comment", "first line: kind: decision | pivot | note"
	if o.mode == editLocalComment {
		title = "Edit local comment"
	}
	if o.mode == editLocalSummary {
		title, hint = "Edit final summary", "follow the repository PR template; describe the final result"
	}
	if o.mode == editReviewBody {
		title, hint = "Edit general review", "submitted with Comment, Approve, or Request changes"
	}
	if o.mode == addInlineReviewComment {
		title, hint = "Add inline review comment", "RIGHT = new code; LEFT = deleted code"
	}
	if o.mode == addRemoteComment {
		title, hint = "Add comment", "posts a GitHub conversation comment"
	}
	if o.mode == editRemoteComment && o.target == "pr-description" {
		title, hint = "Edit PR description", "updates the pull request body on GitHub"
	} else if o.mode == editRemoteComment {
		title, hint = "Edit comment", "updates your GitHub conversation comment"
	}
	lines := []string{stBold.Render(title), stMuted.Render(hint), "", m.localEditor.View()}
	if o.errText != "" {
		lines = append(lines, "", stRedF.Render(o.errText))
	}
	lines = append(lines, "", stMuted.Render("Enter/Shift+Enter newline · Ctrl+S send · Esc cancel"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAccent)).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
}

func (o localDeleteOverlay) render(m Model) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAttention)).
		Padding(1, 3).
		Width(max(24, min(60, m.w-14))).
		Render(stBold.Render("Delete local comment?") + "\n\n" + stFg.Render(o.title) + "\n\n" + stMuted.Render("y confirm · n / Esc cancel"))
}

func (o remoteDeleteOverlay) render(m Model) string {
	preview := strings.ReplaceAll(o.title, "\n", " ")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cDangerEmphasis)).
		Padding(1, 3).
		Width(max(24, min(60, m.w-14))).
		Render(stRedF.Bold(true).Render("Delete GitHub comment?") + "\n\n" + stFg.Render(preview) + "\n\n" + stMuted.Render("y confirm · n / Esc cancel"))
}
