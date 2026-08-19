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
	if !m.detailView.conversationDirty {
		return m.detailView.conversationCache
	}
	commitStatuses := 0
	if m.cache.PR != nil {
		commitStatuses = len(m.cache.PR.Commits)
	}
	items := make([]conversationItem, 0, len(m.detailView.events)+len(m.cache.Comments)+len(m.cache.Activities)+len(m.cache.Reviews)+len(m.cache.ReviewComments)+commitStatuses+1)
	if m.cache.PR == nil && strings.TrimSpace(m.detailView.summary) != "" {
		items = append(items, conversationItem{key: "local-summary", summary: &m.detailView.summary})
	} else if m.cache.PR != nil {
		items = append(items, conversationItem{key: "description:" + m.cache.PR.URL, ts: m.cache.PR.CreatedAt, pr: m.cache.PR})
	}
	eventOccurrences := map[string]int{}
	for i := range m.detailView.events {
		e := &m.detailView.events[i]
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
	for i := range m.outbox {
		entry := &m.outbox[i]
		items = append(items, conversationItem{key: "outbox:" + entry.ID, ts: entry.CreatedAt, outbox: entry})
	}
	for i := range m.cache.Activities {
		activity := &m.cache.Activities[i]
		key := activity.NodeID
		if key == "" {
			key = fmt.Sprintf("%d", activity.ID)
		}
		items = append(items, conversationItem{key: "activity:" + key, ts: activity.CreatedAt, activity: activity})
	}
	for i := range m.cache.Reviews {
		review := &m.cache.Reviews[i]
		// A COMMENTED review with no body is just the wrapper for inline
		// comments; skip it so only meaningful reviews show.
		if strings.EqualFold(review.State, "COMMENTED") && strings.TrimSpace(review.Body) == "" {
			continue
		}
		items = append(items, conversationItem{key: fmt.Sprintf("review:%d", review.ID), ts: review.SubmittedAt, review: review})
	}
	for i := range m.cache.ReviewComments {
		rc := &m.cache.ReviewComments[i]
		items = append(items, conversationItem{key: fmt.Sprintf("review-comment:%d", rc.ID), ts: rc.CreatedAt, reviewComment: rc})
	}
	if m.cache.PR != nil {
		for i := range m.cache.PR.Commits {
			commit := &m.cache.PR.Commits[i]
			items = append(items, conversationItem{key: "commit-ci:" + commit.OID, ts: commit.CommittedDate, prCommit: commit})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		leftPinned, rightPinned := items[i].summary != nil || items[i].pr != nil, items[j].summary != nil || items[j].pr != nil
		if leftPinned != rightPinned {
			return leftPinned
		}
		return conversationTime(items[i].ts).Before(conversationTime(items[j].ts))
	})
	m.detailView.conversationCache, m.detailView.conversationDirty = items, false
	return m.detailView.conversationCache
}

func (m *Model) selectedConversationItem() *conversationItem {
	items := m.conversationItems()
	i := m.detailView.cursors[conversationTab]
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
			m.detailView.cursors[conversationTab] = i
			return
		}
	}
	if n := len(m.conversationItems()); n > 0 && m.detailView.cursors[conversationTab] >= n {
		m.detailView.cursors[conversationTab] = n - 1
	}
}

func (m *Model) activeLen() int {
	switch m.detailView.active {
	case commitsTab:
		return len(m.detailView.commits)
	case conflictsTab:
		return len(m.detailView.mergeReadiness.ConflictFiles)
	case checksTab:
		if m.cache.PR != nil {
			return len(m.cache.PR.Checks)
		}
		return 0
	default:
		return len(m.conversationItems())
	}
}

func (m *Model) buildList() (string, int) {
	switch m.detailView.active {
	case commitsTab:
		return m.buildCommits()
	case conflictsTab:
		return m.buildConflicts()
	case checksTab:
		return m.buildChecks()
	default:
		return m.buildConversation()
	}
}

func (m Model) conversationCounts() string {
	eventCount := 0
	for _, ev := range m.detailView.events {
		if ev.Kind != event.Commit {
			eventCount++
		}
	}
	activityCount := len(m.cache.Activities)
	if m.cache.PR != nil {
		activityCount += len(m.cache.PR.Commits)
	}
	return stMuted.Render(fmt.Sprintf("%d events · %d comments · %d activity", eventCount, len(m.cache.Comments), activityCount))
}

type convRenderKey struct {
	cursor int
	width  int
	items  int
}

func (m *Model) buildConversation() (string, int) {
	items := m.conversationItems()
	key := convRenderKey{cursor: m.detailView.cursors[conversationTab], width: m.list.Width, items: len(items)}
	if m.detailView.conversationRenderValid && m.detailView.conversationRenderKey == key {
		return m.detailView.conversationRender, m.detailView.conversationRenderLine
	}
	out, selectedLine := m.renderConversation(items)
	m.detailView.conversationRender, m.detailView.conversationRenderLine = out, selectedLine
	m.detailView.conversationRenderKey, m.detailView.conversationRenderValid = key, true
	return out, selectedLine
}

func (m *Model) renderConversation(items []conversationItem) (string, int) {
	if len(items) == 0 {
		return stMuted.Render("(no conversation yet — try `live-pr note …`)"), 0
	}
	var lines []string
	selectedLine := 0
	for i, item := range items {
		selected := i == m.detailView.cursors[conversationTab]
		if selected {
			selectedLine = len(lines)
		}
		itemLines := m.conversationItemLines(item, selected)
		lines = append(lines, itemLines...)
		nextCompactActivity := i+1 < len(items) && items[i+1].compactActivity()
		if !item.compactActivity() || !nextCompactActivity {
			lines = append(lines, "")
		}
	}
	return strings.TrimSuffix(strings.Join(lines, "\n"), "\n"), selectedLine
}

// conversationItemLines renders one card. Unselected renders are cached by
// (item key, width): invalidateConversation clears the cache on content
// changes, so cursor moves re-render only the selected card instead of every
// card in the conversation.
func (m *Model) conversationItemLines(item conversationItem, selected bool) []string {
	cacheKey := fmt.Sprintf("%s\x00%d", item.key, m.list.Width)
	if !selected {
		if cached, ok := m.detailView.convItemCache[cacheKey]; ok {
			return cached
		}
	}
	// Render without the accent bar; the selected item is shown by a
	// full-width background band instead.
	var itemLines []string
	switch item.kind() {
	case itemSummary:
		itemLines = m.summaryLines(*item.summary, false, m.list.Width)
	case itemPRDescription:
		itemLines = m.descriptionLines(*item.pr, false, m.list.Width)
	case itemComment:
		itemLines = m.commentLines(*item.comment, false, m.list.Width)
	case itemOutbox:
		itemLines = m.outboxLines(*item.outbox, false, m.list.Width)
	case itemReview:
		itemLines = m.reviewLines(*item.review, false, m.list.Width)
	case itemReviewComment:
		itemLines = m.reviewCommentLines(*item.reviewComment, false, m.list.Width)
	case itemActivity:
		itemLines = m.activityLines(*item.activity, false)
	case itemPRCommit:
		itemLines = m.commitCIActivityLines(*item.prCommit, false)
	case itemEvent:
		itemLines = m.eventLines(*item.event, false, m.list.Width)
	default:
		itemLines = []string{stMuted.Render("(unrenderable item)")}
	}
	if selected {
		for j := range itemLines {
			itemLines[j] = highlightSelectedBg(itemLines[j], m.list.Width)
		}
		return itemLines
	}
	if m.detailView.convItemCache == nil {
		m.detailView.convItemCache = map[string][]string{}
	}
	m.detailView.convItemCache[cacheKey] = itemLines
	return itemLines
}

func (m Model) eventLines(e event.Event, selected bool, width int) []string {
	who := "🤖 agent"
	if e.Author == "user" || (e.Author == "" && e.Kind == event.Note) {
		who = "👤 you"
	}
	meta := " · " + relativeTS(time.Now(), e.TS)
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
	body := m.richBody(pr.Body)
	if strings.TrimSpace(body) == "" {
		body = "(no description provided)"
	}
	header := m.userIcon(pr.Author.Login) + stMuted.Render(" @"+pr.Author.Login+" · description · "+relativeTS(time.Now(), pr.CreatedAt))
	return cardLines(header, md.Render(body, width-7), selected, width, cDescriptionBorder)
}

func (m Model) commentLines(comment gh.Comment, selected bool, width int) []string {
	header := m.userIcon(comment.User.Login) + stMuted.Render(" @"+comment.User.Login+" · comment · "+relativeTS(time.Now(), comment.CreatedAt))
	return cardLines(header, md.Render(m.richBody(comment.Body), width-7), selected, width, cCloudBorder)
}

// reviewLines renders a submitted review verdict with GitHub's semantic color.
func (m Model) reviewLines(review gh.Review, selected bool, width int) []string {
	verdict, style := reviewVerdict(review.State)
	header := m.userIcon(review.User.Login) + stMuted.Render(" @"+review.User.Login+" · ") + style.Render(verdict) + stMuted.Render(" · "+relativeTS(time.Now(), review.SubmittedAt))
	body := strings.TrimSpace(review.Body)
	if body == "" {
		return []string{selectionBar(selected) + header}
	}
	return cardLines(header, md.Render(m.richBody(body), width-7), selected, width, reviewBorder(review.State))
}

// reviewBorder frames a verdict in its own color so an approval or a change
// request is distinguishable from ordinary comments without reading it.
func reviewBorder(state string) string {
	switch strings.ToUpper(state) {
	case "APPROVED":
		return cGreenF
	case "CHANGES_REQUESTED":
		return cRedF
	default:
		return cCloudBorder
	}
}

func (m Model) reviewCommentLines(rc gh.ReviewThreadComment, selected bool, width int) []string {
	loc := rc.Path
	if rc.Line > 0 {
		loc = fmt.Sprintf("%s:%d", rc.Path, rc.Line)
	}
	header := m.userIcon(rc.User.Login) + stMuted.Render(" @"+rc.User.Login+" · review comment · ") + stAccent.Render(loc) + stMuted.Render(" · "+relativeTS(time.Now(), rc.CreatedAt))
	return cardLines(header, md.Render(m.richBody(rc.Body), width-7), selected, width, cBorder)
}

// reviewVerdict maps a review state to its label and GitHub color.
func reviewVerdict(state string) (string, lipgloss.Style) {
	switch strings.ToUpper(state) {
	case "APPROVED":
		return "approved", stGreenF
	case "CHANGES_REQUESTED":
		return "requested changes", stRedF
	case "DISMISSED":
		return "review dismissed", stMuted
	default:
		return "reviewed", stMuted
	}
}

func (m Model) richBody(body string) string {
	if rendered, ok := m.detailView.richBodies[body]; ok {
		return rendered
	}
	return body
}

func (m Model) userIcon(login string) string { return m.userIconOn(login, "") }

func (m Model) userIconOn(login, background string) string {
	color := cMuted
	if avatarColor := m.avatarColors[login]; avatarColor != "" {
		color = avatarColor
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	if background != "" {
		style = style.Background(lipgloss.Color(background))
	}
	return style.Render("●")
}

func (m Model) userLabel(user gh.PRUser) string {
	return m.userIcon(user.Login) + stFg.Render(" @"+user.Login)
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
	glyph, style := activityGlyph(activity.Event)
	summary := style.Render(glyph + " " + activitySummary(activity))
	line := m.userIcon(activity.Actor.Login) + stMuted.Render(" @"+activity.Actor.Login+" ") + summary + stMuted.Render(" · "+relativeTS(time.Now(), activity.CreatedAt))
	return []string{selectionBar(selected) + line}
}

// activityGlyph gives lifecycle events GitHub's semantic color so merged /
// closed / reopened stand out in the feed; other events stay muted.
func activityGlyph(evt string) (string, lipgloss.Style) {
	switch evt {
	case "merged":
		return "⬡", lipgloss.NewStyle().Foreground(lipgloss.Color(cDoneEmphasis))
	case "closed":
		return "⊘", stRedF
	case "reopened":
		return "↺", stGreenF
	default:
		return "•", stMuted
	}
}

func (m Model) commitCIActivityLines(commit gh.PRCommit, selected bool) []string {
	icon, label, style := commitCIStatus(commit.CheckRollupState)
	line := style.Render(icon+" "+label) + stMuted.Render(" · "+shortSHA(commit.OID))
	if commit.MessageHeadline != "" {
		line += stFg.Render(" " + commit.MessageHeadline)
	}
	if commit.CommittedDate != "" {
		line += stMuted.Render(" · " + relativeTS(time.Now(), commit.CommittedDate))
	}
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
