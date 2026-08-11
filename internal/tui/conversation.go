package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/shonenm/live-pr/internal/event"
	gh "github.com/shonenm/live-pr/internal/github"
	md "github.com/shonenm/live-pr/internal/markdown"
)

func (m *Model) conversationItems() []conversationItem {
	if !m.conversationDirty {
		return m.conversationCache
	}
	items := make([]conversationItem, 0, len(m.events)+len(m.cache.Comments)+len(m.cache.Activities)+1)
	if !m.remote && strings.TrimSpace(m.summary) != "" {
		items = append(items, conversationItem{key: "local-summary", summary: &m.summary})
	} else if m.cache.PR != nil {
		items = append(items, conversationItem{key: "description:" + m.cache.PR.URL, ts: m.cache.PR.CreatedAt, pr: m.cache.PR})
	}
	eventOccurrences := map[string]int{}
	for i := range m.events {
		e := &m.events[i]
		if e.Kind != event.Commit {
			baseKey := "event:" + e.ID
			if e.ID == "" {
				baseKey = fmt.Sprintf("event:%q:%q:%q:%q", e.TS, e.Kind, e.Title, e.Body)
			}
			occurrence := eventOccurrences[baseKey]
			eventOccurrences[baseKey]++
			items = append(items, conversationItem{key: fmt.Sprintf("%s:%d", baseKey, occurrence), ts: e.TS, event: e})
		}
	}
	for i := range m.cache.Comments {
		comment := &m.cache.Comments[i]
		key := comment.NodeID
		if key == "" {
			key = fmt.Sprintf("%d", comment.ID)
		}
		items = append(items, conversationItem{key: "comment:" + key, ts: comment.CreatedAt, comment: comment})
	}
	for i := range m.cache.Activities {
		activity := &m.cache.Activities[i]
		key := activity.NodeID
		if key == "" {
			key = fmt.Sprintf("%d", activity.ID)
		}
		items = append(items, conversationItem{key: "activity:" + key, ts: activity.CreatedAt, activity: activity})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return conversationTime(items[i].ts).Before(conversationTime(items[j].ts))
	})
	m.conversationCache, m.conversationDirty = items, false
	return m.conversationCache
}

func (m *Model) selectedConversationItem() *conversationItem {
	items := m.conversationItems()
	i := m.cursors[conversationTab]
	if i < 0 || i >= len(items) {
		return nil
	}
	return &items[i]
}

func (m *Model) selectedConversationKey() string {
	if item := m.selectedConversationItem(); item != nil {
		return item.key
	}
	return ""
}

func (m *Model) restoreConversationSelection(key string) {
	if key == "" {
		return
	}
	for i, item := range m.conversationItems() {
		if item.key == key {
			m.cursors[conversationTab] = i
			return
		}
	}
	if n := len(m.conversationItems()); n > 0 && m.cursors[conversationTab] >= n {
		m.cursors[conversationTab] = n - 1
	}
}

func (m *Model) activeLen() int {
	if m.active == commitsTab {
		return len(m.commits)
	}
	return len(m.conversationItems())
}

func (m *Model) buildList() (string, int) {
	if m.active == commitsTab {
		return m.buildCommits()
	}
	return m.buildConversation()
}

func (m *Model) buildConversation() (string, int) {
	items := m.conversationItems()
	if len(items) == 0 {
		return stMuted.Render("(no conversation yet — try `live-pr note …`)"), 0
	}
	var lines []string
	selectedLine := 0
	for i, item := range items {
		selected := i == m.cursors[conversationTab]
		if selected {
			selectedLine = len(lines)
		}
		if item.summary != nil {
			lines = append(lines, m.summaryLines(*item.summary, selected, m.list.Width)...)
		} else if item.pr != nil {
			lines = append(lines, m.descriptionLines(*item.pr, selected, m.list.Width)...)
		} else if item.comment != nil {
			lines = append(lines, m.commentLines(*item.comment, selected, m.list.Width)...)
		} else if item.activity != nil {
			lines = append(lines, m.activityLines(*item.activity, selected)...)
		} else {
			lines = append(lines, m.eventLines(*item.event, selected, m.list.Width)...)
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), selectedLine
}

func (m Model) eventLines(e event.Event, selected bool, width int) []string {
	who := "🤖 agent"
	if e.Author == "user" || (e.Author == "" && e.Kind == event.Note) {
		who = "👤 you"
	}
	meta := " · " + shortTS(e.TS)
	if e.UpdatedAt != "" {
		meta += " · edited"
	}
	header := stMuted.Render(who+" · ") + kindLabel(e.Kind) + stMuted.Render(meta)
	body := stBold.Render(e.Title)
	if strings.TrimSpace(e.Body) != "" {
		body += "\n" + md.Render(e.Body, width-7)
	}
	return cardLines(header, body, selected, width, cBorder)
}

func (m Model) summaryLines(summary string, selected bool, width int) []string {
	header := stMuted.Render("📝 local PR · final summary")
	return cardLines(header, md.Render(summary, width-7), selected, width, cBorder)
}

func (m Model) descriptionLines(pr gh.PR, selected bool, width int) []string {
	body := pr.Body
	if strings.TrimSpace(body) == "" {
		body = "(no description provided)"
	}
	header := stMuted.Render("💬 @" + pr.Author.Login + " · description · " + shortTS(pr.CreatedAt))
	return cardLines(header, md.Render(body, width-7), selected, width, cCloudBorder)
}

func (m Model) commentLines(comment gh.Comment, selected bool, width int) []string {
	header := stMuted.Render("💬 @" + comment.User.Login + " · comment · " + shortTS(comment.CreatedAt))
	return cardLines(header, md.Render(comment.Body, width-7), selected, width, cCloudBorder)
}

func cardLines(header, body string, selected bool, width int, border string) []string {
	if selected {
		border = cAccent
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Padding(0, 1).
		Width(max(12, width-4)).
		Render(header + "\n" + body)
	bar := selectionBar(selected)
	lines := strings.Split(box, "\n")
	for i := range lines {
		lines[i] = bar + lines[i]
	}
	return lines
}

func (m Model) activityLines(activity gh.Activity, selected bool) []string {
	line := stMuted.Render("● @"+activity.Actor.Login+" ") + stFg.Render(activitySummary(activity)) + stMuted.Render(" · "+shortTS(activity.CreatedAt))
	return []string{selectionBar(selected) + line}
}

func activitySummary(activity gh.Activity) string {
	switch activity.Event {
	case "labeled", "unlabeled":
		return activity.Event + " " + kindLabelText(activity.Label.Name)
	case "assigned", "unassigned":
		return activity.Event + " @" + activity.Assignee.Login
	case "review_requested", "review_request_removed":
		return strings.ReplaceAll(activity.Event, "_", " ") + " @" + activity.RequestedReviewer.Login
	case "renamed":
		return "renamed " + activity.Rename.From + " → " + activity.Rename.To
	case "head_ref_force_pushed":
		return "force-pushed " + shortSHA(activity.CommitID)
	default:
		return strings.ReplaceAll(activity.Event, "_", " ")
	}
}

func kindLabelText(label string) string {
	if label == "" {
		return "label"
	}
	return "`" + label + "`"
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func conversationTime(ts string) time.Time {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", ts, time.Local); err == nil {
		return t
	}
	return time.Time{}
}
