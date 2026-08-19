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

// refreshReviewDraft reloads the pending review draft from disk so the detail
// header badge reflects it; without a PR head there is nothing pending.
func (m *Model) refreshReviewDraft() {
	if err := m.loadReviewDraft(); err != nil {
		m.reviewDraft = gh.ReviewDraft{}
	}
}

// startAddComment (a) posts a conversation comment: a GitHub issue comment when
// the branch has a PR, otherwise a local timeline note. Inline review comments
// live on A; the review verdict + body live on v.
func (m Model) startAddComment() (Model, tea.Cmd) {
	if m.detailView.active != conversationTab {
		m.status = "comments live in the Conversation tab (esc)"
		return m, nil
	}
	if m.detailView.focusDiff || m.detailView.focusExplorer {
		return m, nil
	}
	if m.cache.PR != nil {
		return m.openLocalEditor(localEditOverlay{mode: addRemoteComment}, "")
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
		return m, tea.Batch(fetchRemotePR(m.client, *m.cache.PR, m.targetGeneration, m.cachedDetail()), m.startSpinner())
	}
	return m, tea.Batch(fetchGitHub(m.client, m.detailView.head, m.cache.PR.Number, m.targetGeneration, m.cachedDetail()), m.startSpinner())
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
	file := m.detailView.selectedFile()
	if file == nil {
		m.status = "select a changed file first"
		return m, nil
	}
	value := fmt.Sprintf("path: %s\nline: 1\nside: RIGHT\n\n", file.Path)
	return m.openLocalEditor(localEditOverlay{mode: addInlineReviewComment}, value)
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

// reviewSubmitOverlay is the review verdict picker (comment / approve /
// request changes); typing switches it to the shared localEditor for the
// general review body.
type reviewSubmitOverlay struct {
	event  gh.ReviewEvent
	cursor int
	typing bool
}

func (m Model) openReviewSubmit() (Model, tea.Cmd) {
	if m.reviewSubmitting || m.cache.PR == nil {
		return m, nil
	}
	if err := m.loadReviewDraft(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.overlay = reviewSubmitOverlay{event: gh.ReviewCommentEvent}
	return m, nil
}

func submitReview(client githubClient, draft gh.ReviewDraft, event gh.ReviewEvent) tea.Cmd {
	return func() tea.Msg { return reviewSubmitted{event: event, err: client.SubmitReview(draft, event)} }
}

func (o reviewSubmitOverlay) handleKey(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	if o.typing {
		switch msg.String() {
		case "esc":
			m.localEditor.Blur()
			o.typing = false
			m.overlay = o
			return m, nil
		case "ctrl+s":
			m.reviewDraft.Body = strings.TrimSpace(m.localEditor.Value())
			if o.event == gh.ReviewRequestChangesEvent && m.reviewDraft.Body == "" {
				m.status = "request changes requires a general review body"
				return m, nil
			}
			m.localEditor.Blur()
			m.overlay = nil
			m.reviewSubmitting = true
			m.status = ""
			return m, tea.Batch(submitReview(m.client, m.reviewDraft, o.event), m.startSpinner())
		default:
			var cmd tea.Cmd
			m.localEditor, cmd = m.localEditor.Update(msg)
			return m, cmd
		}
	}
	events := []gh.ReviewEvent{gh.ReviewCommentEvent, gh.ReviewApproveEvent, gh.ReviewRequestChangesEvent}
	switch msg.String() {
	case "up", "k":
		o.cursor = (o.cursor + len(events) - 1) % len(events)
		m.overlay = o
		return m, nil
	case "down", "j":
		o.cursor = (o.cursor + 1) % len(events)
		m.overlay = o
		return m, nil
	case "enter":
		o.event, o.typing = events[o.cursor], true
		next, cmd := m.setupLocalEditor(m.reviewDraft.Body)
		next.overlay = o
		return next, cmd
	case "esc", "q", "n":
		m.overlay = nil
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

func (o reviewSubmitOverlay) render(m Model) string {
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
		if i == o.cursor {
			prefix, style = "▸ ", stAccent.Bold(true)
		}
		lines = append(lines, prefix+style.Render(label))
	}
	if o.typing {
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
