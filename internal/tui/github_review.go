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

func (m Model) startReviewComment() (Model, tea.Cmd) {
	if m.cache.PR == nil {
		m.status = "publish the Local PR before adding a GitHub review"
		return m, nil
	}
	if err := m.loadReviewDraft(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	if m.fileExplorerMode() && m.focusExplorer {
		return m.startInlineReviewComment()
	}
	if m.focusDiff || m.focusExplorer {
		m.status = "inline comments require the built-in Explorer; set [diff].command = \"\""
		return m, nil
	}
	return m.openLocalEditor(editReviewBody, m.reviewDraft.Body, "")
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
	m.reviewSubmitEvent, m.reviewSubmitCursor = gh.ReviewCommentEvent, 0
	return m, nil
}

func submitReview(draft gh.ReviewDraft, event gh.ReviewEvent) tea.Cmd {
	return func() tea.Msg { return reviewSubmitted{event: event, err: gh.New().SubmitReview(draft, event)} }
}

func (m Model) handleReviewSubmitKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.reviewSubmitEvent == "" {
		return m, nil
	}
	events := []gh.ReviewEvent{gh.ReviewCommentEvent, gh.ReviewApproveEvent, gh.ReviewRequestChangesEvent}
	var event gh.ReviewEvent
	switch msg.String() {
	case "up", "k":
		m.reviewSubmitCursor = (m.reviewSubmitCursor + len(events) - 1) % len(events)
		return m, nil
	case "down", "j":
		m.reviewSubmitCursor = (m.reviewSubmitCursor + 1) % len(events)
		return m, nil
	case "enter":
		event = events[m.reviewSubmitCursor]
	case "c":
		event = gh.ReviewCommentEvent
	case "a":
		event = gh.ReviewApproveEvent
	case "x":
		event = gh.ReviewRequestChangesEvent
	case "e":
		m.reviewSubmitEvent = ""
		next, cmd := m.openLocalEditor(editReviewBody, m.reviewDraft.Body, "review-submit")
		next.reviewSubmitCursor = m.reviewSubmitCursor
		return next, cmd
	case "d":
		if len(m.reviewDraft.Comments) == 0 {
			m.status = "review draft has no inline comments"
			return m, nil
		}
		m.reviewDraft.Comments = m.reviewDraft.Comments[:len(m.reviewDraft.Comments)-1]
		if err := gh.SaveReviewDraft(m.reviewDraftPath, m.reviewDraft); err != nil {
			m.status = err.Error()
		} else {
			m.notice = "Removed last inline review comment"
		}
		return m, nil
	case "D":
		if err := os.Remove(m.reviewDraftPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.status = err.Error()
			return m, nil
		}
		m.reviewDraft, m.reviewSubmitEvent = gh.ReviewDraft{}, ""
		m.notice = "Review draft discarded"
		return m, nil
	case "esc", "q", "n":
		m.reviewSubmitEvent = ""
		return m, nil
	default:
		return m, nil
	}
	if event == gh.ReviewRequestChangesEvent && strings.TrimSpace(m.reviewDraft.Body) == "" {
		m.status = "request changes requires a general review body"
		return m, nil
	}
	m.reviewSubmitEvent = ""
	m.reviewSubmitting = true
	m.status = ""
	return m, tea.Batch(submitReview(m.reviewDraft, event), m.startSpinner())
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
	if body := strings.TrimSpace(m.reviewDraft.Body); body != "" {
		lines = append(lines, stMuted.Render("Body: ")+stFg.Render(body))
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
	lines = append(lines, "", stMuted.Render("j/k select · Enter submit · c/a/x shortcut"), stMuted.Render("e edit comment · d remove last inline · D discard draft · Esc cancel"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAttention)).
		Padding(1, 2).
		Width(max(36, min(80, m.w-14))).
		Render(strings.Join(lines, "\n"))
}
