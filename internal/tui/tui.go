// Package tui renders the living PR with Conversation, Files changed, and
// Commits tabs plus a contextual detail pane.
package tui

import (
	"errors"
	"fmt"
	"os"
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
	Up, Down, PrevTab, NextTab, Open, Refresh, Help, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.NextTab, k.Open, k.Refresh, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.PrevTab, k.NextTab}, {k.Open, k.Refresh, k.Help, k.Quit}}
}

var keys = keyMap{
	Up:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	PrevTab: key.NewBinding(key.WithKeys("shift+tab", "h", "left"), key.WithHelp("h/⇧tab", "previous tab")),
	NextTab: key.NewBinding(key.WithKeys("tab", "l", "right"), key.WithHelp("l/tab", "next tab")),
	Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "review commit")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh GitHub")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

// reviewerDone is delivered after the external reviewer process exits.
type reviewerDone struct{ err error }

type githubRefreshed struct {
	pr  gh.PR
	err error
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
		pr, err := gh.New().FindOpen(head)
		return githubRefreshed{pr: pr, err: err}
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

	case githubRefreshed:
		m.refreshing = false
		now := time.Now().UTC().Format(time.RFC3339)
		switch {
		case msg.err == nil:
			m.cache.PR = &msg.pr
			m.cache.FetchedAt = now
			m.githubStatus = "GitHub: updated now"
		case errors.Is(msg.err, gh.ErrPRNotFound):
			m.cache.PR = nil
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
			if m.refreshing {
				return m, nil
			}
			m.refreshing = true
			m.githubStatus = "GitHub: refreshing…"
			return m, fetchGitHub(m.head)
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

func (m Model) activeLen() int {
	switch m.active {
	case filesTab:
		return len(m.files)
	case commitsTab:
		return len(m.commits)
	default:
		return len(m.events)
	}
}

func (m Model) selectedCommitSHA() string {
	switch m.active {
	case commitsTab:
		if i := m.cursors[commitsTab]; i >= 0 && i < len(m.commits) {
			return m.commits[i].SHA
		}
	case conversationTab:
		if i := m.cursors[conversationTab]; i >= 0 && i < len(m.events) {
			e := m.events[i]
			if e.Kind == event.Commit {
				return e.SHA
			}
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
		stMuted.Render(fmt.Sprintf("   · %d commits · %d events", len(m.commits), len(m.events)))
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
	if len(m.events) == 0 {
		return stMuted.Render("(no events yet — try `live-pr note …`)"), 0
	}
	var lines []string
	selectedLine := 0
	for i, e := range m.events {
		if i == m.cursors[conversationTab] {
			selectedLine = len(lines)
		}
		lines = append(lines, m.eventLines(e, i == m.cursors[conversationTab], m.list.Width)...)
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), selectedLine
}

func (m Model) eventLines(e event.Event, selected bool, width int) []string {
	bar := selectionBar(selected)
	who := "🤖 claude-agent"
	if e.Kind == event.Note {
		who = "👤 you"
	}
	var hdr string
	if e.Kind == event.Commit {
		hdr = kindLabel(e.Kind) + " " + stGreenF.Render(e.SHA) + stMuted.Render(" · "+shortTS(e.TS))
	} else {
		hdr = stMuted.Render(who+" · ") + kindLabel(e.Kind) + stMuted.Render(" · "+shortTS(e.TS))
	}
	lines := []string{bar + hdr}
	for _, ln := range wrapLines(e.Title, stBold, width-3) {
		lines = append(lines, bar+ln)
	}
	if body := strings.TrimSpace(e.Body); body != "" {
		for _, para := range strings.Split(body, "\n") {
			for _, ln := range wrapLines(para, stMuted, width-3) {
				lines = append(lines, bar+ln)
			}
		}
	}
	return lines
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
		i := m.cursors[conversationTab]
		if i < 0 || i >= len(m.events) {
			return stMuted.Render("no events")
		}
		e := m.events[i]
		if e.Kind == event.Commit {
			return m.commitDetail(e.SHA)
		}
		head := stMuted.Render("— " + string(e.Kind) + " · no diff —")
		body := lipgloss.NewStyle().Width(m.detail.Width).Render(strings.TrimSpace(e.Body))
		return head + "\n\n" + body
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

func shortTS(ts string) string { return strings.Replace(ts, "T", " ", 1) }
func itoa(n int) string        { return fmt.Sprintf("%d", n) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
