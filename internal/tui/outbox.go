// Comment outbox: conversation comments that failed to post because GitHub
// was unreachable are queued on disk instead of dropped, shown as pending
// cards in the conversation, and re-sent on refresh or the next successful
// detail load.
package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	gh "github.com/shonenm/live-pr/internal/github"
	md "github.com/shonenm/live-pr/internal/markdown"
	"github.com/shonenm/live-pr/internal/store"
)

// refreshOutbox reloads the queued comments for the current PR so the
// conversation shows their pending cards; without a PR there is no outbox.
func (m *Model) refreshOutbox() {
	m.outbox, m.outboxPath = nil, ""
	if m.cache.PR == nil || m.cache.PR.Number <= 0 {
		return
	}
	m.outboxPath = store.CommentOutbox(m.root, m.cache.PR.Number)
	entries, err := store.LoadOutbox(m.outboxPath)
	if err != nil {
		m.status = "outbox: " + err.Error()
		return
	}
	m.outbox = entries
}

// queueOfflineComment moves a network-failed post or edit into the outbox
// instead of dropping the text. Auth and validation failures (StatusHint
// names them) keep the plain error path: queueing cannot fix those.
func (m Model) queueOfflineComment(msg remoteCommentDone) (Model, tea.Cmd, bool) {
	if msg.body == "" || msg.deleted || msg.number <= 0 || gh.StatusHint(msg.err) != "" {
		return m, nil, false
	}
	path := store.CommentOutbox(m.root, msg.number)
	entries, err := store.AppendOutbox(path, store.OutboxEntry{PR: msg.number, Body: msg.body, CommentID: msg.editID})
	currentTarget := msg.generation == m.targetGeneration && m.cache.PR != nil && m.cache.PR.Number == msg.number
	if err != nil {
		if currentTarget {
			m.status = "comment: " + msg.err.Error() + " · queue failed: " + err.Error()
		}
		return m, nil, true
	}
	if !currentTarget {
		return m, nil, true
	}
	m.outboxPath = path
	m.outbox = entries
	m.detailView.invalidateConversation()
	m.status = ""
	m.notice = "Offline · comment queued; sends on refresh (r)"
	m.githubStatus = offlineStatus(msg.err, queuedCount(len(entries)), m.cache.FetchedAt)
	return m, m.sync(), true
}

type outboxFlushed struct {
	generation uint64
	number     int
	posted     []string // IDs of entries that reached GitHub, in queue order
	err        error
}

// startOutboxFlush dispatches the queued comments when there are any and no
// flush is already in flight.
func (m *Model) startOutboxFlush() tea.Cmd {
	if m.outboxFlushing || len(m.outbox) == 0 || m.cache.PR == nil || m.cache.PR.Number <= 0 {
		return nil
	}
	m.outboxFlushing = true
	return flushOutbox(m.client, m.cache.PR.Number, m.outbox, m.targetGeneration)
}

// flushOutbox re-sends queued comments in order and stops at the first
// failure so the remainder keeps its queue position for the next attempt.
func flushOutbox(client githubClient, number int, entries []store.OutboxEntry, generation uint64) tea.Cmd {
	snapshot := slices.Clone(entries)
	return func() tea.Msg {
		msg := outboxFlushed{generation: generation, number: number}
		for _, entry := range snapshot {
			var err error
			if entry.CommentID > 0 {
				err = client.EditIssueComment(entry.CommentID, entry.Body)
			} else {
				err = client.PostIssueComment(number, entry.Body)
			}
			if err != nil {
				msg.err = err
				break
			}
			msg.posted = append(msg.posted, entry.ID)
		}
		return msg
	}
}

func (m Model) handleOutboxFlushed(msg outboxFlushed) (Model, tea.Cmd) {
	m.outboxFlushing = false
	// Posted entries reached GitHub whatever the user is looking at now, so
	// the file update never waits on the generation guard below — leaving
	// them queued would double-post on the next flush.
	if len(msg.posted) > 0 {
		path := store.CommentOutbox(m.root, msg.number)
		if _, err := store.RemoveOutbox(path, msg.posted...); err != nil {
			m.status = "outbox: " + err.Error()
		}
	}
	if msg.generation != m.targetGeneration || m.cache.PR == nil || m.cache.PR.Number != msg.number {
		return m, nil
	}
	m.refreshOutbox()
	m.detailView.invalidateConversation()
	if len(msg.posted) == 0 {
		m.githubStatus = offlineStatus(msg.err, queuedCount(len(m.outbox)), m.cache.FetchedAt)
		return m, m.sync()
	}
	m.notice = fmt.Sprintf("Sent %d queued comment(s)", len(msg.posted))
	if msg.err != nil {
		m.notice += " · " + queuedCount(len(m.outbox)) + " still"
	}
	// Refetch so the delivered comments appear, via the same remote/local
	// split as handleRemoteCommentDone.
	m.targetGeneration++
	if m.remote {
		return m, tea.Batch(fetchRemotePR(m.client, *m.cache.PR, m.targetGeneration, m.cachedDetail()), m.startSpinner())
	}
	return m, tea.Batch(fetchGitHub(m.client, m.detailView.head, m.cache.PR.Number, m.targetGeneration, m.cachedDetail()), m.startSpinner())
}

func queuedCount(n int) string {
	if n == 1 {
		return "1 comment queued"
	}
	return fmt.Sprintf("%d comments queued", n)
}

// outboxLines renders a queued comment as a local card with an attention
// badge, so unsent text stays visible in the conversation instead of being
// silently parked on disk.
func (m Model) outboxLines(entry store.OutboxEntry, selected bool, width int) []string {
	label := "pending comment"
	if entry.CommentID > 0 {
		label = "pending edit"
	}
	header := stMuted.Render("👤 you · ") + stAttention.Render("◌ "+label) + stMuted.Render(" · outbox · "+relativeTS(time.Now(), entry.CreatedAt)+" · sends on refresh (r)")
	return cardLines(header, md.Render(entry.Body, width-8), selected, width, cBorder)
}

// outboxDiscardOverlay is the y/n confirm for dropping a queued comment.
type outboxDiscardOverlay struct {
	id    string
	title string
}

func (o outboxDiscardOverlay) handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.overlay = nil
		entries, err := store.RemoveOutbox(m.outboxPath, o.id)
		if err != nil {
			m.status = "outbox: " + err.Error()
			return m, nil
		}
		m.outbox = entries
		m.detailView.invalidateConversation()
		m.notice = "Queued comment discarded"
		return m, m.sync()
	case "n", "esc", "q":
		m.overlay = nil
	}
	return m, nil
}

func (o outboxDiscardOverlay) render(m Model) string {
	preview := strings.ReplaceAll(o.title, "\n", " ")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cAttention)).
		Padding(1, 3).
		Width(max(24, min(60, m.w-14))).
		Render(stBold.Render("Discard queued comment?") + "\n\n" + stFg.Render(preview) + "\n\n" + stMuted.Render("y confirm · n / Esc cancel"))
}
