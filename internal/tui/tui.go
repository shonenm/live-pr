// Package tui renders the living PR with Conversation, Files changed, and
// Commits tabs plus a contextual detail pane.
package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	md "github.com/shonenm/live-pr/internal/markdown"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/review"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
)

type tab int

const (
	conversationTab tab = iota
	filesTab
	commitsTab
	tabCount
)

type keyMap struct {
	Up, Down, PrevTab, NextTab, Open, Browse, Refresh, Publish, Help, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.NextTab, k.Open, k.Browse, k.Refresh, k.Publish, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.PrevTab, k.NextTab}, {k.Open, k.Browse, k.Refresh, k.Publish, k.Help, k.Quit}}
}

var keys = keyMap{
	Up:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	PrevTab: key.NewBinding(key.WithKeys("shift+tab", "h", "left"), key.WithHelp("h/⇧tab", "previous tab")),
	NextTab: key.NewBinding(key.WithKeys("tab", "l", "right"), key.WithHelp("l/tab", "next tab")),
	Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "review commit")),
	Browse:  key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open comment")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh GitHub")),
	Publish: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "publish PR")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

// reviewerDone is delivered after the external reviewer process exits.
type reviewerDone struct{ err error }

type githubRefreshed struct {
	pr            gh.PR
	comments      []gh.Comment
	activities    []gh.Activity
	err           error
	commentsErr   error
	activitiesErr error
}

type publishDone struct {
	result publish.Result
	err    error
}

type browserDone struct{ err error }

type conversationItem struct {
	key      string
	ts       string
	event    *event.Event
	comment  *gh.Comment
	activity *gh.Activity
}

// Model holds the living-PR view state.
type Model struct {
	title        string
	base, head   string
	events       []event.Event
	files        []git.ChangedFile
	commits      []git.Commit
	active       tab
	cursors      [tabCount]int
	reviewer     string
	status       string
	githubStatus string
	cachePath    string
	cache        gh.Cache
	refreshing   bool
	publishing   bool

	list   viewport.Model
	detail viewport.Model
	help   help.Model
	keys   keyMap
	w, h   int
	ready  bool
}

// New builds a model from the current branch's store.
func New() (Model, error) {
	st, err := store.Discover()
	if err != nil {
		return Model{}, err
	}
	base := git.DefaultBase()
	_, _ = timeline.SyncCommits(st.Timeline(), base)

	events, err := event.Load(st.Timeline())
	if err != nil {
		return Model{}, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })

	commits, commitErr := git.Commits(base)
	files, fileErr := git.ChangedFiles(base)
	status := ""
	if commitErr != nil || fileErr != nil {
		status = "local git data is incomplete"
	}
	cache, cacheErr := gh.LoadCache(st.GitHubCache(), st.Branch)
	if cacheErr != nil {
		cache = gh.NewCache(st.Branch)
		status = "GitHub cache ignored: " + cacheErr.Error()
	}
	githubStatus := "Local only · checking for PR…"
	if cache.PR != nil {
		githubStatus = "GitHub: cached · refreshing…"
	}

	return Model{
		title:        deriveTitle(st.Conclusion(), st.Branch),
		base:         base,
		head:         st.Branch,
		events:       events,
		files:        files,
		commits:      commits,
		reviewer:     config.Load(st.Root).Reviewer,
		status:       status,
		githubStatus: githubStatus,
		cachePath:    st.GitHubCache(),
		cache:        cache,
		refreshing:   true,
		help:         help.New(),
		keys:         keys,
	}, nil
}

// Run launches the TUI.
func Run() error {
	m, err := New()
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	if m.cachePath == "" {
		return nil
	}
	return fetchGitHub(m.head)
}

func fetchGitHub(head string) tea.Cmd {
	return func() tea.Msg {
		client := gh.New()
		pr, err := client.FindOpen(head)
		if err != nil {
			return githubRefreshed{err: err}
		}
		comments, commentsErr := client.IssueComments(pr.Number)
		activities, activitiesErr := client.IssueActivities(pr.Number)
		return githubRefreshed{pr: pr, comments: comments, activities: activities, commentsErr: commentsErr, activitiesErr: activitiesErr}
	}
}

const (
	headerLines = 4
	footerLines = 1
	listRatio   = 52
)

func (m *Model) layout() {
	listW := m.w * listRatio / 100
	if listW < 20 {
		listW = 20
	}
	detailW := m.w - listW - 3
	if detailW < 10 {
		detailW = 10
	}
	bodyH := m.h - headerLines - footerLines
	if bodyH < 3 {
		bodyH = 3
	}
	if !m.ready {
		m.list = viewport.New(listW, bodyH)
		m.detail = viewport.New(detailW, bodyH)
		m.ready = true
	} else {
		m.list.Width, m.list.Height = listW, bodyH
		m.detail.Width, m.detail.Height = detailW, bodyH
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		m.sync()
		return m, nil

	case reviewerDone:
		if msg.err != nil {
			m.status = "reviewer: " + msg.err.Error()
		} else if strings.HasPrefix(m.status, "reviewer:") {
			m.status = ""
		}
		return m, nil

	case browserDone:
		if msg.err != nil {
			m.status = "browser: " + msg.err.Error()
		} else if strings.HasPrefix(m.status, "browser:") {
			m.status = ""
		}
		return m, nil

	case publishDone:
		m.publishing = false
		if msg.err != nil {
			m.status = "publish: " + msg.err.Error()
			return m, nil
		}
		if cache, err := gh.LoadCache(m.cachePath, m.head); err == nil {
			m.cache = cache
		}
		action := "updated"
		if msg.result.Created {
			action = "created"
		}
		m.status = ""
		m.githubStatus = "PR " + action + ": " + msg.result.PR.URL
		return m, nil

	case githubRefreshed:
		m.refreshing = false
		selectedKey := m.selectedConversationKey()
		now := time.Now().UTC().Format(time.RFC3339)
		switch {
		case msg.err == nil:
			m.cache.PR = &msg.pr
			m.cache.FetchedAt = now
			stale := []string{}
			if msg.commentsErr == nil {
				m.cache.Comments = msg.comments
			} else {
				stale = append(stale, "comments")
			}
			if msg.activitiesErr == nil {
				m.cache.Activities = msg.activities
			} else {
				stale = append(stale, "activity")
			}
			m.githubStatus = "GitHub: updated now"
			if len(stale) > 0 {
				m.githubStatus = "GitHub: PR updated · " + strings.Join(stale, "/") + " stale"
			}
		case errors.Is(msg.err, gh.ErrPRNotFound):
			m.cache.PR = nil
			m.cache.Comments = nil
			m.cache.Activities = nil
			m.cache.FetchedAt = now
			m.githubStatus = "Local only · no GitHub PR"
		default:
			m.githubStatus = "Offline · showing cached GitHub data"
		}
		if err := gh.SaveCache(m.cachePath, m.cache); err != nil {
			m.status = "GitHub cache: " + err.Error()
		} else if strings.HasPrefix(m.status, "GitHub cache") {
			m.status = ""
		}
		m.restoreConversationSelection(selectedKey)
		m.sync()
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			return m, nil
		case msg.String() == "1" || msg.String() == "2" || msg.String() == "3":
			m.active = tab(int(msg.String()[0] - '1'))
			m.sync()
			return m, nil
		case key.Matches(msg, m.keys.NextTab):
			m.active = (m.active + 1) % tabCount
			m.sync()
			return m, nil
		case key.Matches(msg, m.keys.PrevTab):
			m.active = (m.active + tabCount - 1) % tabCount
			m.sync()
			return m, nil
		case key.Matches(msg, m.keys.Refresh):
			if m.refreshing || m.publishing {
				return m, nil
			}
			m.refreshing = true
			m.githubStatus = "GitHub: refreshing…"
			return m, fetchGitHub(m.head)
		case key.Matches(msg, m.keys.Publish):
			if m.publishing {
				return m, nil
			}
			if m.refreshing {
				m.status = "wait for GitHub refresh before publishing"
				return m, nil
			}
			m.publishing = true
			m.status = "publishing PR…"
			base := m.base
			return m, func() tea.Msg {
				result, err := publish.Run(publish.Options{Base: base})
				return publishDone{result: result, err: err}
			}
		case key.Matches(msg, m.keys.Browse):
			comment := m.selectedComment()
			if comment == nil || comment.HTMLURL == "" {
				return m, nil
			}
			url := comment.HTMLURL
			return m, func() tea.Msg { return browserDone{err: openURL(url)} }
		case key.Matches(msg, m.keys.Open):
			sha := m.selectedCommitSHA()
			if sha == "" {
				m.status = "select a commit to review"
				return m, nil
			}
			cmd := review.Command(m.reviewer, sha, m.base, m.head)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return reviewerDone{err} })
		case key.Matches(msg, m.keys.Down):
			if m.cursors[m.active] < m.activeLen()-1 {
				m.cursors[m.active]++
				m.sync()
			}
			return m, nil
		case key.Matches(msg, m.keys.Up):
			if m.cursors[m.active] > 0 {
				m.cursors[m.active]--
				m.sync()
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.Update(msg)
	return m, cmd
}

func (m Model) conversationItems() []conversationItem {
	items := make([]conversationItem, 0, len(m.events)+len(m.cache.Comments)+len(m.cache.Activities))
	for i := range m.events {
		e := &m.events[i]
		items = append(items, conversationItem{key: fmt.Sprintf("event:%d", i), ts: e.TS, event: e})
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
	return items
}

func (m Model) selectedConversationItem() *conversationItem {
	items := m.conversationItems()
	i := m.cursors[conversationTab]
	if i < 0 || i >= len(items) {
		return nil
	}
	return &items[i]
}

func (m Model) selectedConversationKey() string {
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

func (m Model) activeLen() int {
	switch m.active {
	case filesTab:
		return len(m.files)
	case commitsTab:
		return len(m.commits)
	default:
		return len(m.conversationItems())
	}
}

func (m Model) selectedComment() *gh.Comment {
	if m.active == conversationTab {
		if item := m.selectedConversationItem(); item != nil {
			return item.comment
		}
	}
	return nil
}

func (m Model) selectedCommitSHA() string {
	switch m.active {
	case commitsTab:
		if i := m.cursors[commitsTab]; i >= 0 && i < len(m.commits) {
			return m.commits[i].SHA
		}
	case conversationTab:
		if item := m.selectedConversationItem(); item != nil && item.event != nil && item.event.Kind == event.Commit {
			return item.event.SHA
		}
	}
	return ""
}

// sync rebuilds both panes for the current tab and selection.
func (m *Model) sync() {
	if !m.ready {
		return
	}
	m.keys.Open.SetEnabled(m.active != filesTab)
	m.keys.Browse.SetEnabled(m.selectedComment() != nil)
	content, selectedLine := m.buildList()
	m.list.SetContent(content)
	if off := selectedLine - 1; off > 0 {
		m.list.SetYOffset(off)
	} else {
		m.list.GotoTop()
	}
	m.detail.SetContent(m.buildDetail())
	m.detail.GotoTop()
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	detail := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
		BorderForeground(lipgloss.Color(cBorder)).PaddingLeft(1).
		Render(m.detail.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), detail)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
}

func (m Model) renderHeader() string {
	badgeText, badgeColor := "Local", cMuted
	if m.cache.PR != nil {
		badgeText, badgeColor = fmt.Sprintf("⇄ #%d %s", m.cache.PR.Number, strings.ToLower(m.cache.PR.State)), cOpen
	}
	badge := lipgloss.NewStyle().
		Background(lipgloss.Color(badgeColor)).Foreground(lipgloss.Color("#ffffff")).
		Padding(0, 1).Render(badgeText)
	l1 := badge + "  " + stBold.Render(m.title)
	l2 := stMuted.Render("⎇ ") + stBold.Render(m.base) + stMuted.Render(" ← ") + stFg.Render(m.head) +
		stMuted.Render(fmt.Sprintf("   · %d commits · %d events · %d comments · %d activity", len(m.commits), len(m.events), len(m.cache.Comments), len(m.cache.Activities)))
	tabs := m.renderTab(conversationTab, "Conversation") + "   " +
		m.renderTab(filesTab, "Files changed "+itoa(len(m.files))) + "   " +
		m.renderTab(commitsTab, "Commits "+itoa(len(m.commits)))
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).Render(strings.Repeat("─", max(0, m.w)))
	return lipgloss.JoinVertical(lipgloss.Left, l1, l2, tabs, rule)
}

func (m Model) renderTab(t tab, label string) string {
	if m.active == t {
		return stBold.Render("● " + label)
	}
	return stMuted.Render(label)
}

func (m Model) buildList() (string, int) {
	switch m.active {
	case filesTab:
		return m.buildFiles()
	case commitsTab:
		return m.buildCommits()
	default:
		return m.buildConversation()
	}
}

func (m Model) buildConversation() (string, int) {
	items := m.conversationItems()
	if len(items) == 0 {
		return stMuted.Render("(no events yet — try `live-pr note …`)"), 0
	}
	var lines []string
	selectedLine := 0
	for i, item := range items {
		selected := i == m.cursors[conversationTab]
		if selected {
			selectedLine = len(lines)
		}
		if item.comment != nil {
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
	if e.Kind == event.Commit {
		line := stMuted.Render("git · ") + stGreenF.Render(e.SHA) + stMuted.Render(" committed ") + stFg.Render(e.Title) + stMuted.Render(" · "+shortTS(e.TS))
		return []string{selectionBar(selected) + line}
	}
	who := "🤖 claude-agent"
	if e.Kind == event.Note {
		who = "👤 you"
	}
	header := stMuted.Render(who+" · ") + kindLabel(e.Kind) + stMuted.Render(" · "+shortTS(e.TS))
	body := stBold.Render(e.Title)
	if strings.TrimSpace(e.Body) != "" {
		body += "\n" + md.Render(e.Body, width-7)
	}
	return cardLines(header, body, selected, width, cBorder)
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
	line := stMuted.Render("● @"+activity.Actor.Login+" ") + activitySummary(activity) + stMuted.Render(" · "+shortTS(activity.CreatedAt))
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

func (m Model) buildFiles() (string, int) {
	if len(m.files) == 0 {
		return stMuted.Render("(no changed files)"), 0
	}
	lines := make([]string, 0, len(m.files))
	for i, f := range m.files {
		path := f.Path
		if f.OldPath != "" {
			path = f.OldPath + " → " + f.Path
		}
		lines = append(lines, selectionBar(i == m.cursors[filesTab])+kindLabelForStatus(f.Status)+" "+path)
	}
	return strings.Join(lines, "\n"), m.cursors[filesTab]
}

func (m Model) buildCommits() (string, int) {
	if len(m.commits) == 0 {
		return stMuted.Render("(no commits in " + m.base + "..HEAD)"), 0
	}
	lines := make([]string, 0, len(m.commits))
	for i, c := range m.commits {
		line := selectionBar(i == m.cursors[commitsTab]) + stGreenF.Render(c.SHA) + " " + c.Subject + stMuted.Render(" · "+shortTS(c.Date))
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), m.cursors[commitsTab]
}

func (m Model) buildDetail() string {
	switch m.active {
	case filesTab:
		i := m.cursors[filesTab]
		if i < 0 || i >= len(m.files) {
			return stMuted.Render("no changed files")
		}
		paths := []string{m.files[i].Path}
		if m.files[i].OldPath != "" {
			paths = append([]string{m.files[i].OldPath}, paths...)
		}
		if d := git.FileDiff(m.base, paths...); d != "" {
			return d
		}
		return stMuted.Render("(no text diff for " + m.files[i].Path + ")")
	case commitsTab:
		return m.commitDetail(m.selectedCommitSHA())
	default:
		item := m.selectedConversationItem()
		if item == nil {
			return stMuted.Render("no events")
		}
		if item.comment != nil {
			head := stMuted.Render("💬 @" + item.comment.User.Login + " · " + shortTS(item.comment.CreatedAt) + " · press o to open")
			return head + "\n\n" + md.Render(item.comment.Body, m.detail.Width)
		}
		if item.activity != nil {
			return stMuted.Render("activity · "+shortTS(item.activity.CreatedAt)) + "\n\n" +
				stFg.Render("@"+item.activity.Actor.Login+" "+activitySummary(*item.activity))
		}
		if item.event.Kind == event.Commit {
			return m.commitDetail(item.event.SHA)
		}
		head := stMuted.Render(string(item.event.Kind) + " · no diff")
		return head + "\n\n" + md.Render(item.event.Body, m.detail.Width)
	}
}

func (m Model) commitDetail(sha string) string {
	if sha == "" {
		return stMuted.Render("no commit selected")
	}
	if d := git.Show(sha); d != "" {
		return d
	}
	return stMuted.Render("(commit " + sha + " not found in this repo)")
}

func (m Model) renderFooter() string {
	if m.status != "" {
		return stRedF.Render(m.status)
	}
	if m.githubStatus != "" {
		return stMuted.Render(m.githubStatus) + "  " + m.help.View(m.keys)
	}
	return m.help.View(m.keys)
}

func selectionBar(selected bool) string {
	if selected {
		return stAccent.Render("▌") + " "
	}
	return "  "
}

func kindLabelForStatus(status string) string {
	color := cMuted
	if strings.HasPrefix(status, "A") {
		color = cGreenF
	} else if strings.HasPrefix(status, "D") {
		color = cRedF
	} else if strings.HasPrefix(status, "M") || strings.HasPrefix(status, "R") {
		color = cDecision
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(status)
}

func deriveTitle(conclusionPath, branch string) string {
	if data, err := os.ReadFile(conclusionPath); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			ln = strings.TrimSpace(strings.TrimLeft(ln, "# "))
			if ln != "" && ln != "<title>" && !strings.HasPrefix(ln, "<current conclusion") {
				return ln
			}
		}
	}
	return branch
}

func wrapLines(text string, style lipgloss.Style, width int) []string {
	if width < 8 {
		width = 8
	}
	return strings.Split(style.Width(width).Render(text), "\n")
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

func browserCommand(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return exec.Command("xdg-open", url)
	}
}

func openURL(url string) error { return browserCommand(url).Run() }

func shortTS(ts string) string { return strings.Replace(ts, "T", " ", 1) }
func itoa(n int) string        { return fmt.Sprintf("%d", n) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
