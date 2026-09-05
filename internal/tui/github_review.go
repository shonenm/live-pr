package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	if m.detailView.focus != focusConversation {
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
	// number, body, and editID echo what postRemoteComment sent, so a
	// network failure can queue the text in the outbox instead of losing it.
	// Deletes and description updates leave them zero and are never queued.
	number int
	body   string
	editID int64
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
		return remoteCommentDone{generation: generation, edited: editID > 0, err: err, number: number, body: body, editID: editID}
	}
}

func (m Model) handleRemoteCommentDone(msg remoteCommentDone) (Model, tea.Cmd) {
	m.remoteCommentBusy = false
	if msg.err != nil {
		// Persist recoverable failures before checking display freshness: the
		// request belongs to its source PR even if navigation advanced the UI.
		if next, cmd, queued := m.queueOfflineComment(msg); queued {
			return next, cmd
		}
	}
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

func updatePRDescription(client githubClient, number int, body string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		file, err := os.CreateTemp("", "live-pr-body-*.md")
		if err != nil {
			return remoteCommentDone{generation: generation, err: err}
		}
		name := file.Name()
		defer func() { _ = os.Remove(name) }()
		if _, err := file.WriteString(body); err != nil {
			_ = file.Close()
			return remoteCommentDone{generation: generation, err: err}
		}
		if err := file.Close(); err != nil {
			return remoteCommentDone{generation: generation, err: err}
		}
		err = client.UpdateBody(number, name)
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
	event         gh.ReviewEvent
	verdictCursor int
	typing        bool
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

func submitReview(client githubClient, draft gh.ReviewDraft, event gh.ReviewEvent, draftPath string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return reviewSubmitted{
			generation: generation,
			draftPath:  draftPath,
			draft:      draft,
			event:      event,
			err:        client.SubmitReview(draft, event),
		}
	}
}

func (o reviewSubmitOverlay) handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
			// Persist the exact submitted draft so completion can remove it only
			// when no comments or body changes were added while the request ran.
			if err := gh.SaveReviewDraft(m.reviewDraftPath, m.reviewDraft); err != nil {
				m.status = "review: save draft: " + err.Error()
				return m, nil
			}
			m.localEditor.Blur()
			m.overlay = nil
			m.reviewSubmitting = true
			m.status = ""
			return m, tea.Batch(submitReview(m.client, m.reviewDraft, o.event, m.reviewDraftPath, m.targetGeneration), m.startSpinner())
		default:
			var cmd tea.Cmd
			m.localEditor, cmd = m.localEditor.Update(msg)
			return m, cmd
		}
	}
	events := []gh.ReviewEvent{gh.ReviewCommentEvent, gh.ReviewApproveEvent, gh.ReviewRequestChangesEvent}
	switch msg.String() {
	case "up", "k":
		o.verdictCursor = (o.verdictCursor + len(events) - 1) % len(events)
		m.overlay = o
		return m, nil
	case "down", "j":
		o.verdictCursor = (o.verdictCursor + 1) % len(events)
		m.overlay = o
		return m, nil
	case "enter":
		o.event, o.typing = events[o.verdictCursor], true
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

// handleMsg feeds non-key messages (paste, cursor blink) to the shared
// editor while the review body is being typed; v1 delivered pastes as key
// messages, so they used to reach the editor through handleKey.
func (o reviewSubmitOverlay) handleMsg(m Model, msg tea.Msg) (Model, tea.Cmd) {
	if !o.typing {
		return m, nil
	}
	var cmd tea.Cmd
	m.localEditor, cmd = m.localEditor.Update(msg)
	return m, cmd
}

func (m Model) handleReviewSubmitted(msg reviewSubmitted) (Model, tea.Cmd) {
	m.reviewSubmitting = false
	currentTarget := m.cache.PR != nil && m.cache.PR.Number == msg.draft.PR && msg.generation == m.targetGeneration
	if msg.err != nil {
		if currentTarget {
			m.status = "review: " + msg.err.Error()
		}
		return m, nil
	}

	persistedData, err := os.ReadFile(msg.draftPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if currentTarget {
			m.status = "review submitted; read draft: " + err.Error()
		}
		return m, nil
	}
	if err == nil {
		var persisted gh.ReviewDraft
		if err := json.Unmarshal(persistedData, &persisted); err != nil {
			if currentTarget {
				m.status = "review submitted; decode draft: " + err.Error()
			}
			return m, nil
		}
		if reflect.DeepEqual(persisted, msg.draft) {
			if err := os.Remove(msg.draftPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				if currentTarget {
					m.status = "review submitted; clear draft: " + err.Error()
				}
				return m, nil
			}
			if currentTarget {
				m.reviewDraft = gh.ReviewDraft{}
			}
		}
	}
	if !currentTarget {
		return m, nil
	}
	m.notice = "Submitted " + string(msg.event) + " review"
	m.githubStatus = "GitHub: review submitted"
	// Refetch so the submitted review shows up without a manual refresh, via
	// the same split as handleRemoteCommentDone: a remote PR must go through
	// fetchRemotePR — fetchGitHub resolves by branch, fails
	// isCurrentTargetPR, and would wipe the remote detail.
	m.targetGeneration++
	if m.remote {
		return m, tea.Batch(fetchRemotePR(m.client, *m.cache.PR, m.targetGeneration, m.cachedDetail()), m.startSpinner())
	}
	return m, tea.Batch(fetchGitHub(m.client, m.detailView.head, m.cache.PR.Number, m.targetGeneration, m.cachedDetail()), m.startSpinner())
}

// bodyWidth is the popup's declared width; its content wraps at bodyWidth
// minus the horizontal padding.
func (o reviewSubmitOverlay) bodyWidth(m Model) int {
	return max(36, min(80, m.w-14))
}

// headerLines builds every body row rendered above the editor (or above the
// closing hint when the verdict picker is shown). cursor measures these rows
// after wrapping, so the caret stays correct however many rows they occupy.
func (o reviewSubmitOverlay) headerLines(m Model) []string {
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
		if i == o.verdictCursor {
			prefix, style = "▸ ", stAccent.Bold(true)
		}
		lines = append(lines, prefix+style.Render(label))
	}
	if o.typing {
		lines = append(lines, "", stMuted.Render("Message"))
	}
	return lines
}

// cursor places the caret inside the editor while the review body is typed.
func (o reviewSubmitOverlay) cursor(m Model) *tea.Cursor {
	if !o.typing {
		return nil
	}
	// The popup wraps its body at the declared width minus padding; measure
	// the rows above the editor after that wrapping.
	header := lipgloss.NewStyle().Width(o.bodyWidth(m) - 4).Render(strings.Join(o.headerLines(m), "\n"))
	return m.editorPopupCursor(lipgloss.Height(header))
}

func (o reviewSubmitOverlay) render(m Model) string {
	lines := o.headerLines(m)
	if o.typing {
		lines = append(lines, m.localEditor.View())
		lines = append(lines, "", stMuted.Render("type message · Ctrl+S submit · Esc back"))
	} else {
		lines = append(lines, "", stMuted.Render("j/k select · Enter write message · Esc cancel"))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAttention)).
		Padding(1, 2).
		Width(o.bodyWidth(m)).
		Render(strings.Join(lines, "\n"))
}
