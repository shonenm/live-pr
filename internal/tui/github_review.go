package tui

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func (m *Model) loadReviewDraft() error {
	if m.cache.PR == nil || m.cache.PR.Number <= 0 || strings.TrimSpace(m.cache.PR.HeadRefOID) == "" {
		return errors.New("GitHub review requires a PR with a known head commit")
	}
	m.reviewDraftPath = store.PullRequestReviewDraft(m.root, m.cache.PR.Number)
	draft, err := gh.LoadReviewDraft(m.reviewDraftPath, m.cache.PR.Number, m.cache.PR.HeadRefOID)
	if err != nil {
		return err
	}
	m.reviewDraft = draft
	return nil
}

// startAddComment (a) posts a conversation comment: a GitHub issue comment when
// the branch has a PR, otherwise a local timeline note. Inline review comments
// live on A; the review verdict + body live on v.
func (m Model) startAddComment() (Model, tea.Cmd) {
	if m.active != conversationTab {
		m.status = "comments live in the Conversation tab (esc)"
		return m, nil
	}
	if m.focusDiff || m.focusExplorer {
		return m, nil
	}
	if m.cache.PR != nil {
		return m.openLocalEditor(addRemoteComment, "", "")
	}
	return m.startLocalComment()
}

type remoteCommentDone struct {
	generation uint64
	edited     bool
	deleted    bool
	err        error
}

// postRemoteComment posts a new conversation comment, or edits one when editID
// is set.
func postRemoteComment(client githubClient, number int, body string, editID int64, generation uint64) tea.Cmd {
	return func() tea.Msg {
		var err error
		if editID > 0 {
			err = client.EditIssueComment(editID, body)
		} else {
			err = client.PostIssueComment(number, body)
		}
		return remoteCommentDone{generation: generation, edited: editID > 0, err: err}
	}
}

func (m Model) handleRemoteCommentDone(msg remoteCommentDone) (Model, tea.Cmd) {
	m.remoteCommentBusy = false
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	if msg.err != nil {
		m.status = "comment: " + msg.err.Error()
		return m, nil
	}
	switch {
	case msg.deleted:
		m.notice = "Comment deleted"
	case msg.edited:
		m.notice = "Comment updated"
	default:
		m.notice = "Comment posted"
	}
	m.status = ""
	if m.cache.PR == nil {
		return m, m.sync()
	}
	m.targetGeneration++
	if m.remote {
		return m, tea.Batch(fetchRemotePR(m.client, *m.cache.PR, m.targetGeneration), m.startSpinner())
	}
	return m, tea.Batch(fetchGitHub(m.client, m.head, m.cache.PR.Number, m.targetGeneration), m.startSpinner())
}

func deleteRemoteComment(client githubClient, id int64, generation uint64) tea.Cmd {
	return func() tea.Msg {
		err := client.DeleteIssueComment(id)
		return remoteCommentDone{generation: generation, deleted: true, err: err}
	}
}

func updatePRDescription(client githubClient, number int, head, body string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		file, err := os.CreateTemp("", "live-pr-body-*.md")
		if err != nil {
			return remoteCommentDone{generation: generation, err: err}
		}
		name := file.Name()
		defer os.Remove(name)
		if _, err := file.WriteString(body); err != nil {
			_ = file.Close()
			return remoteCommentDone{generation: generation, err: err}
		}
		if err := file.Close(); err != nil {
			return remoteCommentDone{generation: generation, err: err}
		}
		err = client.UpdateBody(head, name)
		return remoteCommentDone{generation: generation, edited: true, err: err}
	}
}

func (m Model) startInlineReviewComment() (Model, tea.Cmd) {
	if m.cache.PR == nil {
		m.status = "inline review requires a GitHub PR"
		return m, nil
	}
	if err := m.loadReviewDraft(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	file := m.selectedFile()
	if file == nil {
		m.status = "select a changed file first"
		return m, nil
	}
	value := fmt.Sprintf("path: %s\nline: 1\nside: RIGHT\n\n", file.Path)
	return m.openLocalEditor(addInlineReviewComment, value, "")
}

func parseInlineReviewComment(value string) (gh.ReviewComment, error) {
	lines := strings.Split(value, "\n")
	if len(lines) < 3 {
		return gh.ReviewComment{}, errors.New("expected path, line, side, then comment body")
	}
	path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "path:"))
	lineText := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[1]), "line:"))
	side := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[2]), "side:")))
	line, err := strconv.Atoi(lineText)
	if err != nil {
		return gh.ReviewComment{}, fmt.Errorf("invalid line %q", lineText)
	}
	body := strings.TrimSpace(strings.Join(lines[3:], "\n"))
	comment := gh.ReviewComment{Path: path, Line: line, Side: side, Body: body}
	return comment, gh.ValidateReviewComment(comment)
}

func (m Model) openReviewSubmit() (Model, tea.Cmd) {
	if m.reviewSubmitting || m.cache.PR == nil {
		return m, nil
	}
	if err := m.loadReviewDraft(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.reviewSubmitEvent, m.reviewSubmitCursor, m.reviewSubmitTyping = gh.ReviewCommentEvent, 0, false
	return m, nil
}

func submitReview(client githubClient, draft gh.ReviewDraft, event gh.ReviewEvent) tea.Cmd {
	return func() tea.Msg { return reviewSubmitted{event: event, err: client.SubmitReview(draft, event)} }
}

func (m Model) handleReviewSubmitKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.reviewSubmitEvent == "" {
		return m, nil
	}
	if m.reviewSubmitTyping {
		switch msg.String() {
		case "esc":
			m.localEditor.Blur()
			m.localEditMode, m.localEditTarget, m.localEditError = noLocalEdit, "", ""
			m.reviewSubmitTyping = false
			return m, nil
		case "ctrl+s":
			m.reviewDraft.Body = strings.TrimSpace(m.localEditor.Value())
			if m.reviewSubmitEvent == gh.ReviewRequestChangesEvent && m.reviewDraft.Body == "" {
				m.status = "request changes requires a general review body"
				return m, nil
			}
			m.localEditor.Blur()
			m.localEditMode, m.localEditTarget, m.localEditError = noLocalEdit, "", ""
			m.reviewSubmitTyping = false
			m.reviewSubmitting = true
			m.status = ""
			event := m.reviewSubmitEvent
			m.reviewSubmitEvent = ""
			return m, tea.Batch(submitReview(m.client, m.reviewDraft, event), m.startSpinner())
		default:
			var cmd tea.Cmd
			m.localEditor, cmd = m.localEditor.Update(msg)
			return m, cmd
		}
	}
	events := []gh.ReviewEvent{gh.ReviewCommentEvent, gh.ReviewApproveEvent, gh.ReviewRequestChangesEvent}
	switch msg.String() {
	case "up", "k":
		m.reviewSubmitCursor = (m.reviewSubmitCursor + len(events) - 1) % len(events)
		return m, nil
	case "down", "j":
		m.reviewSubmitCursor = (m.reviewSubmitCursor + 1) % len(events)
		return m, nil
	case "enter":
		m.reviewSubmitEvent = events[m.reviewSubmitCursor]
		next, cmd := m.openLocalEditor(editReviewBody, m.reviewDraft.Body, "review-submit")
		next.reviewSubmitEvent, next.reviewSubmitCursor, next.reviewSubmitTyping = m.reviewSubmitEvent, m.reviewSubmitCursor, true
		return next, cmd
	case "esc", "q", "n":
		m.reviewSubmitEvent = ""
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleReviewSubmitted(msg reviewSubmitted) (Model, tea.Cmd) {
	m.reviewSubmitting = false
	if msg.err != nil {
		m.status = "review: " + msg.err.Error()
		return m, nil
	}
	if err := os.Remove(m.reviewDraftPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.status = "review submitted; clear draft: " + err.Error()
		return m, nil
	}
	m.reviewDraft = gh.ReviewDraft{}
	m.notice = "Submitted " + string(msg.event) + " review"
	m.githubStatus = "GitHub: review submitted"
	return m, m.sync()
}

func (m Model) renderReviewSubmitPopup() string {
	lines := []string{
		stBold.Render(fmt.Sprintf("Submit review for PR #%d?", m.reviewDraft.PR)),
		"",
		stFg.Render(fmt.Sprintf("%d inline comments", len(m.reviewDraft.Comments))),
	}
	for i, comment := range m.reviewDraft.Comments {
		lines = append(lines, stMuted.Render(fmt.Sprintf("%d. %s:%d %s", i+1, comment.Path, comment.Line, comment.Side)))
	}
	labels := []string{"Comment", "Approve", "Request changes"}
	for i, label := range labels {
		prefix := "  "
		style := stFg
		if i == m.reviewSubmitCursor {
			prefix, style = "▸ ", stAccent.Bold(true)
		}
		lines = append(lines, prefix+style.Render(label))
	}
	if m.reviewSubmitTyping {
		lines = append(lines, "", stMuted.Render("Message"), m.localEditor.View())
		lines = append(lines, "", stMuted.Render("type message · Ctrl+S submit · Esc back"))
	} else {
		lines = append(lines, "", stMuted.Render("j/k select · Enter write message · Esc cancel"))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAttention)).
		Padding(1, 2).
		Width(max(36, min(80, m.w-14))).
		Render(strings.Join(lines, "\n"))
}
