// Package tui renders Conversation beside a branch- or commit-scoped review.
package tui

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/diffview"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	md "github.com/shonenm/live-pr/internal/markdown"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
)

type tab int

const (
	conversationTab tab = iota
	commitsTab
	tabCount
)

type keyMap struct {
	Up, Down, Focus, FocusRight, FocusLeft, Commits, Select, Back, Browse, Refresh, Publish, Help, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Focus, k.FocusRight, k.Commits, k.Select, k.Back, k.Browse, k.Refresh, k.Publish, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Focus, k.FocusRight, k.Commits, k.Select, k.Back}, {k.Browse, k.Refresh, k.Publish, k.Help, k.Quit}}
}

var keys = keyMap{
	Up:         key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:       key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	Focus:      key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "toggle focus")),
	FocusRight: key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "focus review")),
	FocusLeft:  key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "focus left")),
	Commits:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "choose commit")),
	Select:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "review commit")),
	Back:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "branch review")),
	Browse:     key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open comment")),
	Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh GitHub")),
	Publish:    key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "publish PR")),
	Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

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

type diffRendered struct {
	key, output, raw string
	err              error
}

type detailContent struct {
	key        string
	raw        string
	renderable bool
}

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
	root         string
	base, head   string
	events       []event.Event
	files        []git.ChangedFile
	commits      []git.Commit
	active       tab
	cursors      [tabCount]int
	reviewSHA    string
	status       string
	githubStatus string
	timelinePath string
	cachePath    string
	cache        gh.Cache
	refreshing   bool
	publishing   bool

	diffDisplay       string
	diffCommand       string
	diffCommitCommand string
	diffTerminal      *embeddedterm.Terminal
	focusDiff         bool
	detailKey         string
	diffCache         map[string]string
	diffPending       map[string]bool

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
	status := ""
	cache, cacheErr := gh.LoadCache(st.GitHubCache(), st.Branch)
	if cacheErr != nil {
		cache = gh.NewCache(st.Branch)
		status = "GitHub cache ignored: " + cacheErr.Error()
	}
	base := git.ResolveBase(cache.Base(git.DefaultBase()))
	_, _ = timeline.SyncCommits(st.Timeline(), base)

	events, err := event.Load(st.Timeline())
	if err != nil {
		return Model{}, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })

	commits, commitErr := git.Commits(base)
	files, fileErr := git.ChangedFiles(base)
	if (commitErr != nil || fileErr != nil) && status == "" {
		status = "local git data is incomplete"
	}
	githubStatus := "Local only · checking for PR…"
	if cache.PR != nil {
		githubStatus = "GitHub: cached · refreshing…"
	}
	cfg := config.Load(st.Root)
	prURL := ""
	if cache.PR != nil {
		prURL = cache.PR.URL
	}
	diffTerminal := embeddedterm.New(cfg.Diff.Command, st.Root, embeddedterm.Environment(base, st.Branch, prURL, ""))

	return Model{
		title:             deriveTitle(st.Conclusion(), st.Branch),
		root:              st.Root,
		base:              base,
		head:              st.Branch,
		events:            events,
		files:             files,
		commits:           commits,
		status:            status,
		githubStatus:      githubStatus,
		timelinePath:      st.Timeline(),
		cachePath:         st.GitHubCache(),
		cache:             cache,
		refreshing:        true,
		diffDisplay:       cfg.Diff.Display,
		diffCommand:       cfg.Diff.Command,
		diffCommitCommand: cfg.CommitReviewCommand(),
		diffTerminal:      diffTerminal,
		diffCache:         map[string]string{},
		diffPending:       map[string]bool{},
		help:              help.New(),
		keys:              keys,
	}, nil
}

// Run launches the TUI.
func Run() error {
	m, err := New()
	if err != nil {
		return err
	}
	final, runErr := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if model, ok := final.(Model); ok {
		model.close()
	} else {
		m.close()
	}
	return runErr
}

func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.cachePath != "" {
		cmds = append(cmds, fetchGitHub(m.head))
	}
	if m.diffTerminal != nil {
		cmds = append(cmds, m.diffTerminal.Init())
	}
	return tea.Batch(cmds...)
}

func (m *Model) close() {
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
}

func (m *Model) useBase(base, prURL string) tea.Cmd {
	base = git.ResolveBase(base)
	if base == "" || base == m.base {
		return nil
	}
	m.base = base
	_, _ = timeline.SyncCommits(m.timelinePath, base)
	if events, err := event.Load(m.timelinePath); err == nil {
		m.events = events
		sort.SliceStable(m.events, func(i, j int) bool { return m.events[i].TS < m.events[j].TS })
	}
	m.commits, _ = git.Commits(base)
	m.files, _ = git.ChangedFiles(base)
	return m.restartReview(m.reviewSHA, prURL)
}

func (m *Model) restartReview(sha, prURL string) tea.Cmd {
	m.reviewSHA = sha
	command := m.diffCommand
	if sha != "" {
		command = m.diffCommitCommand
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	m.diffTerminal = embeddedterm.New(command, m.root, embeddedterm.Environment(m.base, m.head, prURL, sha))
	m.focusDiff = false
	m.layout()
	if m.diffTerminal != nil {
		return m.diffTerminal.Init()
	}
	return nil
}

func (m Model) prURL() string {
	if m.cache.PR != nil {
		return m.cache.PR.URL
	}
	return ""
}

func renderDiff(key, command, raw string, width int) tea.Cmd {
	return func() tea.Msg {
		out, err := diffview.Render(command, raw, width)
		return diffRendered{key: key, output: out, raw: raw, err: err}
	}
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
	headerBaseLines = 3
	footerLines     = 1
	listRatio       = 52
	reviewListRatio = 38
)

func (m *Model) layout() {
	ratio := listRatio
	if m.diffTerminal != nil && m.diffTerminal.Available() {
		ratio = reviewListRatio
	}
	listW := m.w * ratio / 100
	if listW < 20 {
		listW = 20
	}
	detailW := m.w - listW - 3
	if detailW < 10 {
		detailW = 10
	}
	bodyH := m.h - m.headerHeight() - footerLines
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
	if m.diffTerminal != nil {
		m.diffTerminal.Resize(detailW, bodyH)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.diffTerminal != nil && m.diffTerminal.Handles(msg) {
		cmd := m.diffTerminal.Update(msg)
		if !m.diffTerminal.Available() {
			if err := m.diffTerminal.Err(); err != nil {
				m.status = err.Error() + " · showing raw diff"
			}
			m.focusDiff = false
			m.layout()
			return m, tea.Batch(cmd, m.sync())
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, m.sync()

	case diffRendered:
		delete(m.diffPending, msg.key)
		if msg.err != nil {
			m.diffCache[msg.key] = msg.raw
		} else {
			m.diffCache[msg.key] = msg.output
		}
		if m.detailKey == msg.key {
			if msg.err != nil {
				m.status = msg.err.Error()
			} else if strings.HasPrefix(m.status, "diff display") {
				m.status = ""
			}
			m.detail.SetContent(m.diffCache[msg.key])
			m.detail.GotoTop()
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
		m.layout()
		return m, m.sync()

	case githubRefreshed:
		m.refreshing = false
		selectedKey := m.selectedConversationKey()
		now := time.Now().UTC().Format(time.RFC3339)
		var diffCmd tea.Cmd
		switch {
		case msg.err == nil:
			m.cache.PR = &msg.pr
			m.cache.FetchedAt = now
			diffCmd = m.useBase(msg.pr.BaseRefName, msg.pr.URL)
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
		m.layout()
		m.restoreConversationSelection(selectedKey)
		return m, tea.Batch(diffCmd, m.sync())

	case tea.MouseMsg:
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			if local, ok := translateDiffMouse(msg, m.list.Width, m.detail.Width, m.detail.Height, m.headerHeight()); ok {
				m.focusDiff = true
				return m, m.diffTerminal.Update(local)
			}
			if msg.Action == tea.MouseActionPress {
				m.focusDiff = false
			}
		}
		return m, nil

	case tea.KeyMsg:
		if key.Matches(msg, m.keys.Focus) {
			m.focusDiff = !m.focusDiff
			return m, nil
		}
		if !m.focusDiff && key.Matches(msg, m.keys.FocusRight) {
			m.focusDiff = true
			return m, nil
		}
		if m.focusDiff && key.Matches(msg, m.keys.FocusLeft) {
			m.focusDiff = false
			return m, nil
		}
		if m.focusDiff {
			if m.diffTerminal != nil && m.diffTerminal.Available() {
				return m, m.diffTerminal.Update(msg)
			}
			var cmd tea.Cmd
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
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
		case key.Matches(msg, m.keys.Commits):
			m.active = commitsTab
			m.status = "select a commit and press Enter"
			return m, m.sync()
		case key.Matches(msg, m.keys.Back):
			if m.active != commitsTab {
				return m, nil
			}
			m.active = conversationTab
			m.status = ""
			if m.reviewSHA == "" {
				return m, m.sync()
			}
			cmd := m.restartReview("", m.prURL())
			return m, tea.Batch(cmd, m.sync())
		case key.Matches(msg, m.keys.Select):
			if m.active != commitsTab {
				return m, nil
			}
			sha := m.selectedCommitSHA()
			if sha == "" {
				return m, nil
			}
			m.status = ""
			cmd := m.restartReview(sha, m.prURL())
			m.focusDiff = m.diffTerminal != nil && m.diffTerminal.Available()
			return m, tea.Batch(cmd, m.sync())
		case key.Matches(msg, m.keys.Down):
			if m.cursors[m.active] < m.activeLen()-1 {
				m.cursors[m.active]++
				return m, m.sync()
			}
			return m, nil
		case key.Matches(msg, m.keys.Up):
			if m.cursors[m.active] > 0 {
				m.cursors[m.active]--
				return m, m.sync()
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
	if m.active == commitsTab {
		return len(m.commits)
	}
	return len(m.conversationItems())
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
	if i := m.cursors[commitsTab]; m.active == commitsTab && i >= 0 && i < len(m.commits) {
		return m.commits[i].SHA
	}
	return ""
}

// sync rebuilds both panes for the current tab and selection.
func (m *Model) sync() tea.Cmd {
	if !m.ready {
		return nil
	}
	m.keys.Commits.SetEnabled(m.active == conversationTab)
	m.keys.Select.SetEnabled(m.active == commitsTab)
	m.keys.Back.SetEnabled(m.active == commitsTab)
	m.keys.Browse.SetEnabled(m.active == conversationTab && m.selectedComment() != nil)
	content, selectedLine := m.buildList()
	m.list.SetContent(content)
	if off := selectedLine - 1; off > 0 {
		m.list.SetYOffset(off)
	} else {
		m.list.GotoTop()
	}

	return m.syncDetail(m.buildDetail())
}

func (m *Model) syncDetail(detail detailContent) tea.Cmd {
	if strings.HasPrefix(m.status, "diff display") {
		m.status = ""
	}
	m.detailKey = ""
	output := detail.raw
	embedded := m.diffTerminal != nil && m.diffTerminal.Available()
	if !embedded && m.diffDisplay != "" && detail.renderable && detail.raw != "" {
		key := fmt.Sprintf("%s\x00%d\x00%s", m.diffDisplay, m.detail.Width, detail.key)
		m.detailKey = key
		if m.diffCache == nil {
			m.diffCache = map[string]string{}
		}
		if m.diffPending == nil {
			m.diffPending = map[string]bool{}
		}
		if cached, ok := m.diffCache[key]; ok {
			output = cached
		} else if !m.diffPending[key] {
			m.diffPending[key] = true
			m.detail.SetContent(output)
			m.detail.GotoTop()
			return renderDiff(key, m.diffDisplay, detail.raw, m.detail.Width)
		}
	}
	m.detail.SetContent(output)
	m.detail.GotoTop()
	return nil
}

func translateDiffMouse(msg tea.MouseMsg, listWidth, detailWidth, detailHeight, headerHeight int) (tea.MouseMsg, bool) {
	contentX := listWidth + 2 // detail left border + padding
	if msg.X < contentX || msg.X >= contentX+detailWidth ||
		msg.Y < headerHeight || msg.Y >= headerHeight+detailHeight {
		return tea.MouseMsg{}, false
	}
	msg.X -= contentX
	msg.Y -= headerHeight
	return msg, true
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	detailContent := m.detail.View()
	borderColor := cBorder
	if m.diffTerminal != nil && m.diffTerminal.Available() {
		detailContent = m.diffTerminal.View(m.detail.Width, m.detail.Height)
		if m.focusDiff {
			borderColor = cAccent
		}
	}
	detail := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
		BorderForeground(lipgloss.Color(borderColor)).PaddingLeft(1).
		Render(detailContent)
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), detail)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
}

func (m Model) headerHeight() int {
	if m.cache.PR != nil {
		return headerBaseLines + 1
	}
	return headerBaseLines
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
	scope := fmt.Sprintf("%d files changed", len(m.files))
	if m.reviewSHA != "" {
		scope = "commit " + m.reviewSHA
	}
	l2 := stMuted.Render("⎇ ") + stBold.Render(m.base) + stMuted.Render(" ← ") + stFg.Render(m.head) +
		stMuted.Render(fmt.Sprintf("   · %s · %d commits · %d events · %d comments · %d activity", scope, len(m.commits), len(m.events), len(m.cache.Comments), len(m.cache.Activities)))
	lines := []string{l1, l2}
	if m.cache.PR != nil {
		lines = append(lines, m.renderPRMeta(*m.cache.PR))
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).Render(strings.Repeat("─", max(0, m.w))))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderPRMeta(pr gh.PR) string {
	assignees := stMuted.Render("👤 unassigned")
	if len(pr.Assignees) > 0 {
		users := make([]string, 0, len(pr.Assignees))
		for _, user := range pr.Assignees {
			users = append(users, "@"+user.Login)
		}
		assignees = stMuted.Render("👤 ") + stFg.Render(strings.Join(users, " "))
	}
	labels := stMuted.Render("🏷 no labels")
	if len(pr.Labels) > 0 {
		pills := make([]string, 0, len(pr.Labels))
		for _, label := range pr.Labels {
			pills = append(pills, labelPill(label))
		}
		labels = stMuted.Render("🏷 ") + strings.Join(pills, " ")
	}
	line := "  " + assignees + "   " + labels
	if m.w > 0 {
		return ansi.Truncate(line, m.w, "…")
	}
	return line
}

func labelPill(label gh.PRLabel) string {
	background, foreground := cBorder, cFg
	color := strings.TrimPrefix(label.Color, "#")
	if rgb, err := strconv.ParseUint(color, 16, 24); err == nil && len(color) == 6 {
		background = "#" + color
		foreground = contrastingLabelForeground(rgb)
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(background)).Foreground(lipgloss.Color(foreground)).Padding(0, 1).Render(label.Name)
}

func contrastingLabelForeground(background uint64) string {
	const dark uint64 = 0x0d1117
	bgLuminance := relativeLuminance(background)
	whiteContrast := (1.0 + 0.05) / (bgLuminance + 0.05)
	darkLuminance := relativeLuminance(dark)
	darkContrast := (math.Max(bgLuminance, darkLuminance) + 0.05) / (math.Min(bgLuminance, darkLuminance) + 0.05)
	if darkContrast > whiteContrast {
		return "#0d1117"
	}
	return "#ffffff"
}

func relativeLuminance(rgb uint64) float64 {
	channel := func(value uint64) float64 {
		v := float64(value) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	r := channel((rgb >> 16) & 0xff)
	g := channel((rgb >> 8) & 0xff)
	b := channel(rgb & 0xff)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func (m Model) buildList() (string, int) {
	if m.active == commitsTab {
		return m.buildCommits()
	}
	return m.buildConversation()
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

func (m Model) buildDetail() detailContent {
	if m.reviewSHA != "" {
		return m.commitDetail(m.reviewSHA)
	}
	if d := git.FileDiff(m.base); d != "" {
		return detailContent{key: "range:" + m.base, raw: d, renderable: true}
	}
	return detailContent{raw: stMuted.Render("(no changes in " + m.base + "...HEAD)")}
}

func (m Model) commitDetail(sha string) detailContent {
	if sha == "" {
		return detailContent{raw: stMuted.Render("no commit selected")}
	}
	if d := git.Show(sha); d != "" {
		return detailContent{key: "commit:" + sha, raw: d, renderable: true}
	}
	return detailContent{raw: stMuted.Render("(commit " + sha + " not found in this repo)")}
}

func (m Model) renderFooter() string {
	if m.status != "" {
		return stRedF.Render(m.status)
	}
	if m.focusDiff {
		hint := stMuted.Render("Review focused · q / Shift+Tab: left pane")
		if m.githubStatus != "" {
			return stMuted.Render(m.githubStatus) + "  " + hint
		}
		return hint
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
