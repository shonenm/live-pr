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

type screen int

type prView uint8

const (
	allPRsView prView = iota
	reviewRequestedView
	assignedView
	authoredView
	needsMeView
	prViewCount
)

const (
	conversationTab tab = iota
	commitsTab
	tabCount
)

const (
	detailScreen screen = iota
	prListScreen
)

type keyMap struct {
	Up, Down, PreviewUp, PreviewDown, PrevView, NextView, Filter, ToggleStack, Focus, FocusRight, FocusLeft, Commits, Select, Back, PRList, Browse, Refresh, Publish, Merge, Checkout, Close, Help, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.PreviewUp, k.PreviewDown, k.PrevView, k.NextView, k.Filter, k.ToggleStack, k.Focus, k.FocusRight, k.Commits, k.Select, k.Back, k.PRList, k.Browse, k.Refresh, k.Publish, k.Merge, k.Checkout, k.Close, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.PreviewUp, k.PreviewDown, k.PrevView, k.NextView, k.Filter, k.ToggleStack, k.Focus, k.FocusRight, k.Commits, k.Select, k.Back, k.PRList}, {k.Browse, k.Refresh, k.Publish, k.Merge, k.Checkout, k.Close, k.Help, k.Quit}}
}

var keys = keyMap{
	Up:          key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:        key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	PreviewUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "preview up")),
	PreviewDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "preview down")),
	PrevView:    key.NewBinding(key.WithKeys("["), key.WithHelp("[", "previous view")),
	NextView:    key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next view")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	ToggleStack: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "collapse stack")),
	Focus:       key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "toggle focus")),
	FocusRight:  key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "focus review")),
	FocusLeft:   key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "focus left")),
	Commits:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "choose commit")),
	Select:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "review commit")),
	Back:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "branch review")),
	PRList:      key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "PR list")),
	Browse:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open on GitHub")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh GitHub")),
	Publish:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "publish PR")),
	Merge:       key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "merge PR")),
	Checkout:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "checkout PR")),
	Close:       key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "close PR")),
	Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:        key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

type prListRefreshed struct {
	generation uint64
	viewer     string
	prs        []gh.PR
	err        error
}

type remoteLoaded struct {
	generation    uint64
	pr            gh.PR
	headRef       string
	comments      []gh.Comment
	activities    []gh.Activity
	refErr        error
	commentsErr   error
	activitiesErr error
}

type githubRefreshed struct {
	generation    uint64
	pr            gh.PR
	comments      []gh.Comment
	activities    []gh.Activity
	err           error
	commentsErr   error
	activitiesErr error
}

type publishDone struct {
	generation uint64
	result     publish.Result
	err        error
}

type browserDone struct{ err error }

type prAction uint8

const (
	noPRAction prAction = iota
	mergePR
	checkoutPR
	closePR
)

type prActionDone struct {
	action prAction
	pr     gh.PR
	number int
	err    error
}

type diffRendered struct {
	generation       uint64
	key, output, raw string
	err              error
}

type detailContent struct {
	key        string
	raw        string
	renderable bool
}

type stackEntry struct {
	pr    gh.PR
	depth int
}

type prStack struct {
	id      string
	title   string
	order   int
	entries []stackEntry
}

type conversationItem struct {
	key      string
	ts       string
	pr       *gh.PR
	event    *event.Event
	comment  *gh.Comment
	activity *gh.Activity
}

// Model holds the living-PR view state.
type Model struct {
	screen                    screen
	title                     string
	root                      string
	currentBranch             string
	defaultBranch             string
	base, head                string
	headRev                   string
	events                    []event.Event
	files                     []git.ChangedFile
	commits                   []git.Commit
	active                    tab
	cursors                   [tabCount]int
	reviewSHA                 string
	status                    string
	notice                    string
	githubStatus              string
	timelinePath              string
	cachePath                 string
	cache                     gh.Cache
	navigator                 gh.NavigatorCache
	navigatorPath             string
	allPRs                    []gh.PR
	filteredPRs               []gh.PR
	openPRs                   []gh.PR
	viewerLogin               string
	prView                    prView
	filterQuery               string
	filterBeforeEdit          string
	filterSelectionBeforeEdit int
	filterEditing             bool
	prStacks                  []prStack
	collapsedStacks           map[string]bool
	prCursor                  int
	localAvailable            bool
	localTitle                string
	localStats                git.ChangeStats
	localCommitCount          int
	autoOpenCurrent           bool
	refreshing                bool
	listRefreshing            bool
	publishing                bool
	pendingPRAction           prAction
	prActionRunning           prAction
	prActionNumber            int
	prActionPR                gh.PR
	prListGeneration          uint64
	remote                    bool
	targetGeneration          uint64

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

// New builds a navigator-aware model without creating branch state unless the
// current checkout is routed to local detail.
func New() (Model, error) {
	root, err := git.RepoRoot()
	if err != nil {
		return Model{}, err
	}
	branch, err := git.CurrentBranch()
	if err != nil {
		return Model{}, err
	}
	cfg := config.Load(root)
	navigatorPath := store.NavigatorCache(root)
	navigator, navErr := gh.LoadNavigatorCache(navigatorPath)
	status := ""
	if navErr != nil {
		navigator = gh.NewNavigatorCache()
		status = "PR list cache ignored: " + navErr.Error()
	}
	st := store.ForBranch(root, branch)
	cache, cacheErr := gh.LoadCache(st.GitHubCache(), branch)
	if cacheErr != nil {
		cache = gh.NewCache(branch)
		status = "GitHub cache ignored: " + cacheErr.Error()
	}
	defaultRef := git.DefaultBase()
	base := git.ResolveBase(cache.Base(defaultRef))
	files, _ := git.ChangedFiles(base)
	currentPR := cache.PR
	if currentPR != nil && !isCurrentPR(*currentPR, branch) && !cache.ExplicitCheckout {
		currentPR = nil
		cache = gh.NewCache(branch)
	}
	if currentPR == nil {
		for i := range navigator.PRs {
			if isCurrentPR(navigator.PRs[i], branch) {
				currentPR = &navigator.PRs[i]
				break
			}
		}
	}
	defaultBranch := strings.TrimPrefix(defaultRef, "origin/")
	localDetail := shouldOpenLocal(branch, defaultBranch, currentPR != nil, st.HasData(), len(files) > 0)

	m := Model{
		screen:            prListScreen,
		root:              root,
		currentBranch:     branch,
		defaultBranch:     defaultBranch,
		base:              base,
		head:              branch,
		headRev:           "HEAD",
		status:            status,
		navigator:         navigator,
		navigatorPath:     navigatorPath,
		openPRs:           navigator.PRs,
		viewerLogin:       navigator.ViewerLogin,
		autoOpenCurrent:   branch != "HEAD" && branch != defaultBranch,
		listRefreshing:    true,
		prListGeneration:  1,
		diffDisplay:       cfg.Diff.Display,
		diffCommand:       cfg.Diff.Command,
		diffCommitCommand: cfg.CommitReviewCommand(),
		diffCache:         map[string]string{},
		diffPending:       map[string]bool{},
		collapsedStacks:   map[string]bool{},
		help:              newHelp(),
		keys:              keys,
	}
	if localDetail {
		if err := m.loadLocal(st, cache, currentPR); err != nil {
			return Model{}, err
		}
	}
	m.applyPRFilters(0)
	return m, nil
}

func isCurrentPR(pr gh.PR, branch string) bool {
	return !pr.IsCrossRepository && (pr.HeadRefName == "" || pr.HeadRefName == branch)
}

func (m Model) isCurrentTargetPR(pr gh.PR) bool {
	return isCurrentPR(pr, m.currentBranch) || (!m.remote && m.cache.ExplicitCheckout && m.cache.PR != nil && m.cache.PR.Number == pr.Number)
}

func (m Model) explicitPRNumber() int {
	if m.cache.ExplicitCheckout && m.cache.PR != nil {
		return m.cache.PR.Number
	}
	return 0
}

func shouldOpenLocal(branch, defaultBranch string, hasPR, hasData, hasChanges bool) bool {
	return branch != "HEAD" && branch != defaultBranch && (hasPR || hasData || hasChanges)
}

func (m *Model) loadLocal(st *store.Store, cache gh.Cache, hintedPR *gh.PR) error {
	m.targetGeneration++
	if err := st.Ensure(); err != nil {
		return err
	}
	if cache.PR == nil && hintedPR != nil && hintedPR.Number > 0 {
		pr := *hintedPR
		cache.PR = &pr
	}
	base := git.ResolveBase(cache.Base(git.DefaultBase()))
	_, _ = timeline.SyncCommits(st.Timeline(), base)
	events, err := event.Load(st.Timeline())
	if err != nil {
		return err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	commits, commitErr := git.Commits(base)
	files, fileErr := git.ChangedFiles(base)
	stats, _ := git.DiffStats(base, "HEAD")
	if commitErr != nil || fileErr != nil {
		m.status = "local git data is incomplete"
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	prURL := ""
	if cache.PR != nil {
		prURL = cache.PR.URL
	}
	m.screen = detailScreen
	m.remote = false
	m.title = deriveTitle(st.Conclusion(), st.Branch)
	m.localAvailable, m.localTitle = true, m.title
	m.localStats, m.localCommitCount = stats, len(commits)
	m.base, m.head, m.headRev = base, st.Branch, "HEAD"
	m.events, m.files, m.commits = events, files, commits
	m.timelinePath, m.cachePath, m.cache = st.Timeline(), st.GitHubCache(), cache
	m.githubStatus = "Local only · checking for PR…"
	if cache.PR != nil {
		m.githubStatus = "GitHub: cached · refreshing…"
	}
	m.refreshing, m.publishing = true, false
	m.diffTerminal = embeddedterm.New(m.diffCommand, m.root, embeddedterm.Environment(base, st.Branch, "HEAD", prURL, ""))
	m.focusDiff, m.active, m.reviewSHA = false, conversationTab, ""
	m.layout()
	return nil
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
	cmds := []tea.Cmd{fetchPRList(m.prListGeneration)}
	if m.screen == detailScreen && !m.remote && m.cachePath != "" {
		cmds = append(cmds, fetchGitHub(m.head, m.explicitPRNumber(), m.targetGeneration))
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

func (m *Model) advanceAsyncGenerations(previous Model) {
	m.targetGeneration = previous.targetGeneration + 1
	m.prListGeneration = previous.prListGeneration + 1
}

func (m *Model) useBase(base, prURL string) tea.Cmd {
	base = git.ResolveBase(base)
	if base == "" || base == m.base {
		return nil
	}
	m.base = base
	if !m.remote {
		_, _ = timeline.SyncCommits(m.timelinePath, base)
		if events, err := event.Load(m.timelinePath); err == nil {
			m.events = events
			sort.SliceStable(m.events, func(i, j int) bool { return m.events[i].TS < m.events[j].TS })
		}
	}
	m.commits, _ = git.CommitsRange(base, m.headRev)
	m.files, _ = git.ChangedFilesRange(base, m.headRev)
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
	m.diffTerminal = embeddedterm.New(command, m.root, embeddedterm.Environment(m.base, m.head, m.headRev, prURL, sha))
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

func renderDiff(generation uint64, key, command, raw string, width int) tea.Cmd {
	return func() tea.Msg {
		out, err := diffview.Render(command, raw, width)
		return diffRendered{generation: generation, key: key, output: out, raw: raw, err: err}
	}
}

func fetchPRList(generation uint64) tea.Cmd {
	return func() tea.Msg {
		list, err := gh.New().ListOpen()
		return prListRefreshed{generation: generation, viewer: list.ViewerLogin, prs: list.PRs, err: err}
	}
}

func runPRAction(action prAction, pr gh.PR) tea.Cmd {
	return func() tea.Msg {
		client := gh.New()
		var err error
		switch action {
		case mergePR:
			err = client.Merge(pr.Number, pr.HeadRefOID)
		case checkoutPR:
			err = client.Checkout(pr.Number)
		case closePR:
			err = client.Close(pr.Number)
		}
		return prActionDone{action: action, pr: pr, number: pr.Number, err: err}
	}
}

func fetchGitHub(head string, number int, generation uint64) tea.Cmd {
	return func() tea.Msg {
		client := gh.New()
		var pr gh.PR
		var err error
		if number > 0 {
			pr, err = client.Find(number)
		} else {
			pr, err = client.FindOpen(head)
		}
		if err != nil {
			return githubRefreshed{generation: generation, err: err}
		}
		comments, commentsErr := client.IssueComments(pr.Number)
		activities, activitiesErr := client.IssueActivities(pr.Number)
		return githubRefreshed{generation: generation, pr: pr, comments: comments, activities: activities, commentsErr: commentsErr, activitiesErr: activitiesErr}
	}
}

func fetchRemotePR(pr gh.PR, generation uint64) tea.Cmd {
	return func() tea.Msg {
		headRef, refErr := git.FetchPull(pr.Number, pr.BaseRefName, pr.HeadRefOID)
		client := gh.New()
		comments, commentsErr := client.IssueComments(pr.Number)
		activities, activitiesErr := client.IssueActivities(pr.Number)
		return remoteLoaded{generation: generation, pr: pr, headRef: headRef, comments: comments, activities: activities, refErr: refErr, commentsErr: commentsErr, activitiesErr: activitiesErr}
	}
}

func (m *Model) openRemote(pr gh.PR) tea.Cmd {
	m.targetGeneration++
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	m.screen, m.remote = detailScreen, true
	m.title = pr.Title
	m.base = git.ResolveBase(pr.BaseRefName)
	m.head = pr.HeadRefName
	m.headRev = fmt.Sprintf("refs/live-pr/pulls/%d/head", pr.Number)
	m.events, m.commits, m.files = nil, nil, nil
	m.timelinePath, m.cachePath = "", ""
	m.cache = gh.NewCache(pr.HeadRefName)
	m.cache.PR = &pr
	if snapshot, ok := m.navigator.Snapshot(pr.Number); ok {
		m.cache.Comments = snapshot.Comments
		m.cache.Activities = snapshot.Activities
		m.cache.FetchedAt = snapshot.FetchedAt
	}
	m.reviewSHA, m.active, m.focusDiff = "", conversationTab, false
	m.diffTerminal = nil
	m.refreshing, m.publishing = true, false
	m.status = "loading PR refs…"
	m.githubStatus = "GitHub: cached · refreshing selected PR…"
	m.layout()
	return tea.Batch(fetchRemotePR(pr, m.targetGeneration), m.sync())
}

const (
	headerBaseLines = 3
	footerLines     = 1
	listRatio       = 52
	reviewListRatio = 38
)

func (m *Model) layout() {
	if m.screen == prListScreen {
		bodyH := max(3, m.h-4)
		available := max(2, m.w-3)
		listW := max(10, available*reviewListRatio/100)
		if available-listW < 10 {
			listW = max(1, available-10)
		}
		detailW := max(1, available-listW)
		if !m.ready {
			m.list = viewport.New(listW, bodyH)
			m.detail = viewport.New(detailW, bodyH)
			m.ready = true
		} else {
			m.list.Width, m.list.Height = listW, bodyH
			m.detail.Width, m.detail.Height = detailW, bodyH
		}
		return
	}
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
		m.help.Width = msg.Width
		m.layout()
		return m, m.sync()

	case prListRefreshed:
		if msg.generation != m.prListGeneration {
			return m, nil
		}
		m.listRefreshing = false
		if msg.err != nil {
			m.githubStatus = "Offline · showing cached PR list"
			return m, m.sync()
		}
		selectedNumber := m.selectedPRNumber()
		m.viewerLogin = msg.viewer
		m.navigator.ViewerLogin = msg.viewer
		m.navigator.PRs = msg.prs
		m.applyPRFilters(selectedNumber)
		m.navigator.FetchedAt = time.Now().UTC().Format(time.RFC3339)
		if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
			m.status = "PR list cache: " + err.Error()
		}
		m.restorePRSelection(selectedNumber)
		m.githubStatus = "GitHub: PR list updated"
		if m.screen == prListScreen && m.autoOpenCurrent {
			m.autoOpenCurrent = false
			for i := range m.openPRs {
				if m.isCurrentTargetPR(m.openPRs[i]) {
					st := store.ForBranch(m.root, m.currentBranch)
					cache, _ := gh.LoadCache(st.GitHubCache(), m.currentBranch)
					if err := m.loadLocal(st, cache, &m.openPRs[i]); err != nil {
						m.status = err.Error()
						break
					}
					var cmds []tea.Cmd
					cmds = append(cmds, fetchGitHub(m.currentBranch, m.explicitPRNumber(), m.targetGeneration), m.sync())
					if m.diffTerminal != nil {
						cmds = append(cmds, m.diffTerminal.Init())
					}
					return m, tea.Batch(cmds...)
				}
			}
		}
		return m, m.sync()

	case diffRendered:
		if msg.generation != m.targetGeneration {
			return m, nil
		}
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

	case prActionDone:
		m.prActionRunning = noPRAction
		if msg.err != nil {
			m.status = fmt.Sprintf("PR #%d: %v", msg.number, msg.err)
			return m, nil
		}
		if msg.action == mergePR || msg.action == closePR {
			if msg.action == mergePR {
				m.notice = fmt.Sprintf("Merge submitted for PR #%d", msg.number)
			} else {
				m.notice = fmt.Sprintf("Closed PR #%d", msg.number)
			}
			m.listRefreshing = true
			m.prListGeneration++
			m.githubStatus = "GitHub: refreshing PR list…"
			return m, fetchPRList(m.prListGeneration)
		}
		m.close()
		next, err := New()
		if err != nil {
			m.status = "checkout reload: " + err.Error()
			return m, nil
		}
		if msg.pr.Number > 0 {
			cache := gh.NewCache(next.currentBranch)
			cache.PR, cache.ExplicitCheckout = &msg.pr, true
			if err := next.loadLocal(store.ForBranch(next.root, next.currentBranch), cache, &msg.pr); err != nil {
				m.status = "checkout reload: " + err.Error()
				return m, nil
			}
			if err := gh.SaveCache(next.cachePath, next.cache); err != nil {
				m.status = "checkout cache: " + err.Error()
				return m, nil
			}
		}
		next.w, next.h = m.w, m.h
		next.advanceAsyncGenerations(m)
		next.notice = fmt.Sprintf("Checked out PR #%d", msg.number)
		next.layout()
		return next, tea.Batch(next.Init(), next.sync())

	case browserDone:
		if msg.err != nil {
			m.status = "browser: " + msg.err.Error()
		} else if strings.HasPrefix(m.status, "browser:") {
			m.status = ""
		}
		return m, nil

	case publishDone:
		if msg.generation != m.targetGeneration {
			return m, nil
		}
		m.publishing = false
		if msg.err != nil {
			m.status = "publish: " + msg.err.Error()
			return m, nil
		}
		selectedKey := m.selectedConversationKey()
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
		m.restoreConversationSelection(selectedKey)
		return m, m.sync()

	case remoteLoaded:
		if msg.generation != m.targetGeneration {
			return m, nil
		}
		m.refreshing = false
		selectedKey := m.selectedConversationKey()
		now := time.Now().UTC().Format(time.RFC3339)
		m.cache.PR = &msg.pr
		if msg.commentsErr == nil {
			m.cache.Comments = msg.comments
		}
		if msg.activitiesErr == nil {
			m.cache.Activities = msg.activities
		}
		m.cache.FetchedAt = now
		m.navigator.SetSnapshot(gh.PRSnapshot{PR: msg.pr, Comments: m.cache.Comments, Activities: m.cache.Activities, FetchedAt: now})
		if err := gh.SaveNavigatorCache(m.navigatorPath, m.navigator); err != nil {
			m.status = "PR list cache: " + err.Error()
		}
		if msg.refErr != nil {
			m.status = msg.refErr.Error()
			m.githubStatus = "GitHub: Conversation updated · review ref unavailable"
			m.restoreConversationSelection(selectedKey)
			return m, m.sync()
		}
		m.headRev = msg.headRef
		m.base = git.ResolveBase(msg.pr.BaseRefName)
		m.commits, _ = git.CommitsRange(m.base, m.headRev)
		m.files, _ = git.ChangedFilesRange(m.base, m.headRev)
		m.status = ""
		stale := []string{}
		if msg.commentsErr != nil {
			stale = append(stale, "comments")
		}
		if msg.activitiesErr != nil {
			stale = append(stale, "activity")
		}
		m.githubStatus = "GitHub: selected PR updated"
		if len(stale) > 0 {
			m.githubStatus += " · " + strings.Join(stale, "/") + " stale"
		}
		m.diffTerminal = embeddedterm.New(m.diffCommand, m.root, embeddedterm.Environment(m.base, m.head, m.headRev, msg.pr.URL, ""))
		m.layout()
		m.restoreConversationSelection(selectedKey)
		cmds := []tea.Cmd{m.sync()}
		if m.diffTerminal != nil {
			cmds = append(cmds, m.diffTerminal.Init())
		}
		return m, tea.Batch(cmds...)

	case githubRefreshed:
		if msg.generation != m.targetGeneration {
			return m, nil
		}
		if msg.err == nil && !m.isCurrentTargetPR(msg.pr) {
			msg.err = gh.ErrPRNotFound
		}
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
		if errors.Is(msg.err, gh.ErrPRNotFound) && len(m.files) == 0 && !store.ForBranch(m.root, m.currentBranch).HasData() {
			m.targetGeneration++
			if m.diffTerminal != nil {
				m.diffTerminal.Close()
			}
			m.diffTerminal = nil
			m.screen = prListScreen
			m.layout()
			return m, m.sync()
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
		if m.screen == prListScreen {
			if m.filterEditing {
				selected := m.selectedPRNumber()
				switch msg.String() {
				case "enter":
					m.filterEditing, m.filterBeforeEdit, m.filterSelectionBeforeEdit = false, "", 0
				case "esc":
					selected = m.filterSelectionBeforeEdit
					m.filterQuery, m.filterEditing, m.filterBeforeEdit, m.filterSelectionBeforeEdit = m.filterBeforeEdit, false, "", 0
				case "ctrl+c":
					return m, tea.Quit
				case "backspace":
					runes := []rune(m.filterQuery)
					if len(runes) > 0 {
						m.filterQuery = string(runes[:len(runes)-1])
					}
				default:
					if msg.Type == tea.KeyRunes {
						m.filterQuery += string(msg.Runes)
					} else if msg.String() == " " {
						m.filterQuery += " "
					} else {
						return m, nil
					}
				}
				m.applyPRFilters(selected)
				return m, m.sync()
			}
			if m.pendingPRAction != noPRAction {
				switch msg.String() {
				case "y":
					action, pr := m.pendingPRAction, m.prActionPR
					m.pendingPRAction = noPRAction
					m.prActionRunning = action
					m.notice = ""
					return m, runPRAction(action, pr)
				case "n", "q", "esc":
					m.pendingPRAction, m.prActionNumber, m.prActionPR = noPRAction, 0, gh.PR{}
					return m, nil
				case "ctrl+c":
					return m, tea.Quit
				default:
					return m, nil
				}
			}
			if m.prActionRunning != noPRAction {
				if msg.String() == "ctrl+c" {
					return m, tea.Quit
				}
				return m, nil
			}
			switch {
			case key.Matches(msg, m.keys.Filter):
				m.filterBeforeEdit, m.filterSelectionBeforeEdit, m.filterEditing = m.filterQuery, m.selectedPRNumber(), true
				return m, nil
			case msg.String() == "esc" && m.filterQuery != "":
				selected := m.selectedPRNumber()
				m.filterQuery = ""
				m.applyPRFilters(selected)
				return m, m.sync()
			case key.Matches(msg, m.keys.PrevView):
				selected := m.selectedPRNumber()
				m.prView = (m.prView + prViewCount - 1) % prViewCount
				m.applyPRFilters(selected)
				return m, m.sync()
			case key.Matches(msg, m.keys.NextView):
				selected := m.selectedPRNumber()
				m.prView = (m.prView + 1) % prViewCount
				m.applyPRFilters(selected)
				return m, m.sync()
			case key.Matches(msg, m.keys.Quit):
				return m, tea.Quit
			case key.Matches(msg, m.keys.Merge):
				if pr := m.selectedPR(); pr != nil && pr.Number > 0 && pr.HeadRefOID != "" {
					m.pendingPRAction, m.prActionNumber, m.prActionPR = mergePR, pr.Number, *pr
					m.status, m.notice = "", ""
				}
				return m, nil
			case key.Matches(msg, m.keys.Checkout):
				if pr := m.selectedPR(); pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) {
					m.pendingPRAction, m.prActionNumber, m.prActionPR = checkoutPR, pr.Number, *pr
					m.status, m.notice = "", ""
				}
				return m, nil
			case key.Matches(msg, m.keys.Close):
				if pr := m.selectedPR(); pr != nil && pr.Number > 0 {
					m.pendingPRAction, m.prActionNumber, m.prActionPR = closePR, pr.Number, *pr
					m.status, m.notice = "", ""
				}
				return m, nil
			case key.Matches(msg, m.keys.ToggleStack):
				if stack, ok := m.stackForPR(m.selectedPRNumber()); ok {
					collapsing := !m.collapsedStacks[stack.id]
					m.collapsedStacks[stack.id] = collapsing
					selected := m.selectedPRNumber()
					if collapsing {
						selected = stack.entries[0].pr.Number
					}
					m.applyPRFilters(selected)
					return m, m.sync()
				}
				return m, nil
			case key.Matches(msg, m.keys.Refresh):
				if m.listRefreshing {
					return m, nil
				}
				m.listRefreshing = true
				m.prListGeneration++
				m.githubStatus = "GitHub: refreshing PR list…"
				return m, fetchPRList(m.prListGeneration)
			case key.Matches(msg, m.keys.PreviewDown):
				m.detail.HalfPageDown()
				return m, nil
			case key.Matches(msg, m.keys.PreviewUp):
				m.detail.HalfPageUp()
				return m, nil
			case key.Matches(msg, m.keys.Down):
				if m.prCursor < len(m.openPRs)-1 {
					m.prCursor++
					return m, m.sync()
				}
			case key.Matches(msg, m.keys.Up):
				if m.prCursor > 0 {
					m.prCursor--
					return m, m.sync()
				}
			case key.Matches(msg, m.keys.Select):
				pr := m.selectedPR()
				if pr == nil {
					return m, nil
				}
				if !m.isCurrentTargetPR(*pr) {
					return m, m.openRemote(*pr)
				}
				st := store.ForBranch(m.root, m.currentBranch)
				cache, _ := gh.LoadCache(st.GitHubCache(), m.currentBranch)
				if err := m.loadLocal(st, cache, pr); err != nil {
					m.status = err.Error()
					return m, nil
				}
				cmds := []tea.Cmd{fetchGitHub(m.currentBranch, m.explicitPRNumber(), m.targetGeneration), m.sync()}
				if m.diffTerminal != nil {
					cmds = append(cmds, m.diffTerminal.Init())
				}
				return m, tea.Batch(cmds...)
			}
			return m, nil
		}
		if key.Matches(msg, m.keys.PRList) {
			m.targetGeneration++
			if m.diffTerminal != nil {
				m.diffTerminal.Close()
			}
			m.diffTerminal = nil
			m.focusDiff = false
			m.screen = prListScreen
			m.autoOpenCurrent = false
			m.refreshing, m.publishing = false, false
			m.active = conversationTab
			m.status = ""
			m.applyPRFilters(m.selectedPRNumber())
			m.layout()
			return m, m.sync()
		}
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
			if m.remote && m.cache.PR != nil {
				return m, fetchRemotePR(*m.cache.PR, m.targetGeneration)
			}
			return m, fetchGitHub(m.head, m.explicitPRNumber(), m.targetGeneration)
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
			generation := m.targetGeneration
			return m, func() tea.Msg {
				result, err := publish.Run(publish.Options{Base: base})
				return publishDone{generation: generation, result: result, err: err}
			}
		case key.Matches(msg, m.keys.Browse):
			url := m.selectedBrowseURL()
			if url == "" {
				return m, nil
			}
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
	items := make([]conversationItem, 0, len(m.events)+len(m.cache.Comments)+len(m.cache.Activities)+1)
	if m.cache.PR != nil {
		items = append(items, conversationItem{key: "description:" + m.cache.PR.URL, ts: m.cache.PR.CreatedAt, pr: m.cache.PR})
	}
	eventOccurrences := map[string]int{}
	for i := range m.events {
		e := &m.events[i]
		if e.Kind != event.Commit {
			baseKey := fmt.Sprintf("event:%q:%q:%q:%q", e.TS, e.Kind, e.Title, e.Body)
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

func (m Model) selectedBrowseURL() string {
	if m.active != conversationTab {
		return ""
	}
	item := m.selectedConversationItem()
	if item == nil {
		return ""
	}
	if item.pr != nil {
		return item.pr.URL
	}
	if item.comment != nil {
		return item.comment.HTMLURL
	}
	return ""
}

func (v prView) String() string {
	switch v {
	case reviewRequestedView:
		return "Review requested"
	case assignedView:
		return "Assigned"
	case authoredView:
		return "Authored"
	case needsMeView:
		return "Needs me"
	default:
		return "All"
	}
}

func (m Model) matchesView(pr gh.PR, view prView) bool {
	if view == allPRsView {
		return true
	}
	if pr.Number == 0 {
		return view == authoredView
	}
	authored := strings.EqualFold(pr.Author.Login, m.viewerLogin)
	assigned := hasLogin(pr.Assignees, m.viewerLogin)
	reviewRequested := pr.ViewerReviewRequested || hasLogin(pr.ReviewRequests, m.viewerLogin)
	switch view {
	case reviewRequestedView:
		return reviewRequested
	case assignedView:
		return assigned
	case authoredView:
		return authored
	case needsMeView:
		return assigned || reviewRequested
	default:
		return true
	}
}

func hasLogin(users []gh.PRUser, login string) bool {
	if login == "" {
		return false
	}
	for _, user := range users {
		if strings.EqualFold(user.Login, login) {
			return true
		}
	}
	return false
}

func (m *Model) applyPRFilters(selectedNumber int) {
	if m.collapsedStacks == nil {
		m.collapsedStacks = map[string]bool{}
	}
	m.allPRs = m.withLocalPR(m.navigator.PRs)
	m.filteredPRs = make([]gh.PR, 0, len(m.allPRs))
	for _, pr := range m.allPRs {
		if m.matchesView(pr, m.prView) && matchesPRFilter(pr, m.filterQuery, m.viewerLogin) {
			m.filteredPRs = append(m.filteredPRs, pr)
		}
	}
	m.prStacks = buildPRStacks(m.filteredPRs)
	m.openPRs = make([]gh.PR, 0, len(m.filteredPRs))
	for _, stack := range m.prStacks {
		entries := stack.entries
		if len(entries) > 1 && m.collapsedStacks[stack.id] {
			entries = entries[:1]
		}
		for _, entry := range entries {
			m.openPRs = append(m.openPRs, entry.pr)
		}
	}
	m.restorePRSelection(selectedNumber)
}

func buildPRStacks(prs []gh.PR) []prStack {
	if len(prs) == 0 {
		return nil
	}
	head := make(map[string]int, len(prs))
	for i, pr := range prs {
		if pr.HeadRefName == "" {
			continue
		}
		if _, exists := head[pr.HeadRefName]; exists {
			head[pr.HeadRefName] = -1 // ambiguous branch names must not invent a parent
		} else {
			head[pr.HeadRefName] = i
		}
	}
	parents := make([]int, len(prs))
	children := make([][]int, len(prs))
	for i := range parents {
		parents[i] = -1
		if parent, ok := head[prs[i].BaseRefName]; ok && parent >= 0 && parent != i {
			parents[i] = parent
			children[parent] = append(children[parent], i)
		}
	}
	visited := make([]bool, len(prs))
	stacks := make([]prStack, 0, len(prs))
	var addStack func(int)
	addStack = func(root int) {
		if visited[root] {
			return
		}
		stack := prStack{order: root}
		var walk func(int, int)
		walk = func(index, depth int) {
			if visited[index] {
				return
			}
			visited[index] = true
			if index < stack.order {
				stack.order = index
			}
			stack.entries = append(stack.entries, stackEntry{pr: prs[index], depth: depth})
			for _, child := range children[index] {
				walk(child, depth+1)
			}
		}
		walk(root, 0)
		rootPR := stack.entries[0].pr
		stack.id = fmt.Sprintf("pr:%d", rootPR.Number)
		if rootPR.Number == 0 {
			stack.id = "branch:" + rootPR.HeadRefName
		}
		stack.title = rootPR.HeadRefName
		if stack.title == "" {
			stack.title = rootPR.Title
		}
		stacks = append(stacks, stack)
	}
	for i, parent := range parents {
		if parent == -1 {
			addStack(i)
		}
	}
	for i := range prs {
		addStack(i) // cycle/duplicate safety
	}
	sort.SliceStable(stacks, func(i, j int) bool { return stacks[i].order < stacks[j].order })
	return stacks
}

func (m Model) stackForPR(number int) (prStack, bool) {
	for _, stack := range m.prStacks {
		if len(stack.entries) < 2 {
			continue
		}
		for _, entry := range stack.entries {
			if entry.pr.Number == number {
				return stack, true
			}
		}
	}
	return prStack{}, false
}

func matchesPRFilter(pr gh.PR, query, viewer string) bool {
	for _, token := range strings.Fields(strings.ToLower(query)) {
		key, value, structured := strings.Cut(token, ":")
		if structured {
			me := value == "@me"
			if me {
				if viewer == "" {
					return false
				}
				value = strings.ToLower(viewer)
			}
			switch key {
			case "author":
				if !strings.EqualFold(pr.Author.Login, value) {
					return false
				}
				continue
			case "assignee":
				if !hasLogin(pr.Assignees, value) {
					return false
				}
				continue
			case "review-requested":
				if !(me && pr.ViewerReviewRequested) && !hasLogin(pr.ReviewRequests, value) {
					return false
				}
				continue
			case "label":
				matched := false
				for _, label := range pr.Labels {
					matched = matched || strings.EqualFold(label.Name, value)
				}
				if !matched {
					return false
				}
				continue
			case "draft":
				if (value == "true") != pr.IsDraft {
					return false
				}
				continue
			case "ci":
				if health, _ := checkHealth(pr.Checks); health != value {
					return false
				}
				continue
			case "merge":
				conflicting := pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY"
				if value != "conflicting" || !conflicting {
					return false
				}
				continue
			}
		}
		haystack := strings.ToLower(fmt.Sprintf("#%d %s %s %s %s", pr.Number, pr.Title, pr.HeadRefName, pr.BaseRefName, pr.Author.Login))
		for _, label := range pr.Labels {
			haystack += " " + strings.ToLower(label.Name)
		}
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func (m Model) withLocalPR(prs []gh.PR) []gh.PR {
	items := append([]gh.PR(nil), prs...)
	if !m.localAvailable {
		return items
	}
	for _, pr := range items {
		if m.isCurrentTargetPR(pr) {
			return items
		}
	}
	title := m.localTitle
	if title == "" {
		title = m.currentBranch
	}
	local := gh.PR{Title: title, State: "LOCAL", BaseRefName: m.defaultBranch, HeadRefName: m.currentBranch, ChangedFiles: m.localStats.Files, Additions: m.localStats.Additions, Deletions: m.localStats.Deletions, CommitCount: m.localCommitCount}
	return append([]gh.PR{local}, items...)
}

func (m Model) selectedPR() *gh.PR {
	if m.prCursor < 0 || m.prCursor >= len(m.openPRs) {
		return nil
	}
	return &m.openPRs[m.prCursor]
}

func (m Model) selectedPRNumber() int {
	if pr := m.selectedPR(); pr != nil {
		return pr.Number
	}
	return 0
}

func (m *Model) restorePRSelection(number int) {
	if number != 0 {
		for i := range m.openPRs {
			if m.openPRs[i].Number == number {
				m.prCursor = i
				return
			}
		}
	}
	if len(m.openPRs) == 0 {
		m.prCursor = 0
	} else if m.prCursor >= len(m.openPRs) {
		m.prCursor = len(m.openPRs) - 1
	}
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
	if m.screen == prListScreen {
		m.keys.Select.SetEnabled(true)
		m.keys.PreviewUp.SetEnabled(true)
		m.keys.PreviewDown.SetEnabled(true)
		m.keys.PrevView.SetEnabled(true)
		m.keys.NextView.SetEnabled(true)
		m.keys.Filter.SetEnabled(true)
		_, stacked := m.stackForPR(m.selectedPRNumber())
		m.keys.ToggleStack.SetEnabled(stacked)
		m.keys.Focus.SetEnabled(false)
		m.keys.FocusRight.SetEnabled(false)
		m.keys.Commits.SetEnabled(false)
		m.keys.Back.SetEnabled(false)
		m.keys.PRList.SetEnabled(false)
		m.keys.Browse.SetEnabled(false)
		m.keys.Publish.SetEnabled(false)
		pr := m.selectedPR()
		m.keys.Merge.SetEnabled(pr != nil && pr.Number > 0 && pr.HeadRefOID != "" && m.prActionRunning == noPRAction)
		m.keys.Checkout.SetEnabled(pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) && m.prActionRunning == noPRAction)
		m.keys.Close.SetEnabled(pr != nil && pr.Number > 0 && m.prActionRunning == noPRAction)
		content, selectedLine := m.buildPRListRows()
		m.list.SetContent(content)
		m.detail.SetContent(m.buildPRPreview())
		m.detail.GotoTop()
		if off := selectedLine - 1; off > 0 {
			m.list.SetYOffset(off)
		} else {
			m.list.GotoTop()
		}
		return nil
	}
	m.keys.PreviewUp.SetEnabled(false)
	m.keys.PreviewDown.SetEnabled(false)
	m.keys.PrevView.SetEnabled(false)
	m.keys.NextView.SetEnabled(false)
	m.keys.Filter.SetEnabled(false)
	m.keys.ToggleStack.SetEnabled(false)
	m.keys.Focus.SetEnabled(true)
	m.keys.FocusRight.SetEnabled(true)
	m.keys.Commits.SetEnabled(!m.remote && m.active == conversationTab)
	m.keys.PRList.SetEnabled(true)
	m.keys.Select.SetEnabled(m.active == commitsTab)
	m.keys.Back.SetEnabled(m.active == commitsTab)
	m.keys.Browse.SetEnabled(m.selectedBrowseURL() != "")
	m.keys.Publish.SetEnabled(!m.remote)
	m.keys.Merge.SetEnabled(false)
	m.keys.Checkout.SetEnabled(false)
	m.keys.Close.SetEnabled(false)
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
			return renderDiff(m.targetGeneration, key, m.diffDisplay, detail.raw, m.detail.Width)
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
	if m.screen == prListScreen {
		preview := lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
			BorderForeground(lipgloss.Color(cBorder)).PaddingLeft(1).
			Render(m.detail.View())
		body := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), preview)
		return lipgloss.JoinVertical(lipgloss.Left, m.renderPRListHeader(), body, m.renderFooter())
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

func (m Model) renderPRListHeader() string {
	views := make([]string, 0, prViewCount)
	for view := allPRsView; view < prViewCount; view++ {
		label := fmt.Sprintf("%s %d", view, m.viewCount(view))
		style := stMuted
		if view == m.prView {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(cFg)).Background(lipgloss.Color(cSelectedBg)).Bold(true).Padding(0, 1)
		}
		views = append(views, style.Render(label))
	}
	line1 := stBold.Render("Pull requests") + "  " + strings.Join(views, " ")
	filter := stMuted.Render("/ filter · [/] views · space stacks")
	if m.filterEditing {
		filter = stAccent.Render("Filter: ") + stFg.Render(m.filterQuery+"▌")
	} else if m.filterQuery != "" {
		filter = stAccent.Render("Filter: ") + stFg.Render(m.filterQuery) + stMuted.Render(" · Esc clear")
	}
	line2 := filter + stMuted.Render(fmt.Sprintf("   · %d listed · current %s", len(m.filteredPRs), m.currentBranch))
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).Render(strings.Repeat("─", max(0, m.w)))
	return lipgloss.JoinVertical(lipgloss.Left, ansi.Truncate(line1, max(1, m.w), "…"), ansi.Truncate(line2, max(1, m.w), "…"), rule)
}

func (m Model) viewCount(view prView) int {
	count := 0
	for _, pr := range m.allPRs {
		if m.matchesView(pr, view) {
			count++
		}
	}
	return count
}

func (m Model) headerHeight() int {
	if m.screen == prListScreen {
		return 3
	}
	if m.cache.PR != nil {
		return headerBaseLines + 1
	}
	return headerBaseLines
}

func (m Model) renderHeader() string {
	badgeText, badgeColor := "Local", cBorder
	if m.cache.PR != nil {
		badgeText, badgeColor = fmt.Sprintf("⇄ #%d %s", m.cache.PR.Number, strings.ToLower(m.cache.PR.State)), prStateBadgeColor(m.cache.PR.State)
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

func prStateBadgeColor(state string) string {
	switch strings.ToUpper(state) {
	case "OPEN":
		return cOpen
	case "MERGED":
		return cDoneEmphasis
	case "CLOSED":
		return cDangerEmphasis
	default:
		return cClosed
	}
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

func (m Model) buildPRPreview() string {
	pr := m.selectedPR()
	if pr == nil {
		return stMuted.Render("Select a pull request to preview it.")
	}
	width := max(20, m.detail.Width-2)
	identifier := "Local PR"
	if pr.Number > 0 {
		identifier = fmt.Sprintf("#%d", pr.Number)
	}
	lines := []string{
		stMuted.Render(identifier) + "  " + stBold.Render(pr.Title),
		stMuted.Render(pr.BaseRefName + " ← " + pr.HeadRefName),
		"",
		stBold.Render("Status"),
		"  " + mergeSummary(*pr) + "   " + checkSummary(pr.Checks),
	}
	if pr.ReviewDecision != "" {
		lines = append(lines, "  "+reviewSummary(pr.ReviewDecision))
	}
	lines = append(lines,
		"",
		stBold.Render("Size"),
		stFg.Render(fmt.Sprintf("  %d files   ", pr.ChangedFiles))+stGreenF.Render(fmt.Sprintf("+%d", pr.Additions))+stFg.Render("   ")+stRedF.Render(fmt.Sprintf("-%d", pr.Deletions))+stFg.Render(fmt.Sprintf("   %d commits", pr.CommitCount)),
		stFg.Render(fmt.Sprintf("  %d comments", pr.CommentCount)),
		"",
		stBold.Render("Metadata"),
		"  "+previewPeople(*pr),
	)
	if len(pr.Labels) > 0 {
		pills := make([]string, 0, len(pr.Labels))
		for _, label := range pr.Labels {
			pills = append(pills, labelPill(label))
		}
		lines = append(lines, "  "+strings.Join(pills, " "))
	}
	if pr.UpdatedAt != "" {
		lines = append(lines, "  "+stMuted.Render("updated "+shortTS(pr.UpdatedAt)))
	}
	lines = append(lines, "")
	if pr.Number == 0 {
		lines = append(lines, "  "+stMuted.Render("(local PR has no GitHub conversation yet)"))
	} else {
		body := pr.Body
		if strings.TrimSpace(body) == "" {
			body = "(no description provided)"
		}
		header := stMuted.Render("💬 @" + pr.Author.Login + " · description · " + shortTS(pr.CreatedAt))
		lines = append(lines, cardLines(header, previewMarkdown(body, width-7, 10), false, width, cCloudBorder)...)
		if len(pr.Conversation) > 0 {
			comment := pr.Conversation[0]
			header = stMuted.Render("💬 @" + comment.Author.Login + " · comment · " + shortTS(comment.CreatedAt))
			lines = append(lines, "")
			lines = append(lines, cardLines(header, previewMarkdown(comment.Body, width-7, 5), false, width, cCloudBorder)...)
		}
	}
	return strings.Join(lines, "\n")
}

func previewPeople(pr gh.PR) string {
	parts := []string{}
	if pr.Author.Login != "" {
		parts = append(parts, "author @"+pr.Author.Login)
	}
	if len(pr.Assignees) > 0 {
		users := make([]string, 0, len(pr.Assignees))
		for _, user := range pr.Assignees {
			users = append(users, "@"+user.Login)
		}
		parts = append(parts, "assigned "+strings.Join(users, " "))
	}
	if len(parts) == 0 {
		return stMuted.Render("unassigned")
	}
	return stFg.Render(strings.Join(parts, " · "))
}

func reviewSummary(decision string) string {
	label := "review " + strings.ToLower(strings.ReplaceAll(decision, "_", " "))
	switch strings.ToUpper(decision) {
	case "APPROVED":
		return stGreenF.Render(label)
	case "CHANGES_REQUESTED":
		return stRedF.Render(label)
	case "REVIEW_REQUIRED":
		return stAttention.Render(label)
	default:
		return stMuted.Render(label)
	}
}

func mergeSummary(pr gh.PR) string {
	if pr.Number == 0 {
		return stMuted.Render("local")
	}
	state := strings.ToLower(strings.ReplaceAll(pr.MergeStateStatus, "_", " "))
	if pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return stRedF.Render("conflicts")
	}
	if state == "" {
		state = strings.ToLower(pr.Mergeable)
	}
	if state == "" {
		state = "merge unknown"
	}
	if pr.Mergeable == "MERGEABLE" && (pr.MergeStateStatus == "CLEAN" || pr.MergeStateStatus == "UNSTABLE") {
		return stGreenF.Render("mergeable")
	}
	switch pr.MergeStateStatus {
	case "BLOCKED":
		return stRedF.Render(state)
	case "BEHIND", "HAS_HOOKS":
		return stAttention.Render(state)
	default:
		return stMuted.Render(state)
	}
}

func checkHealth(checks []gh.PRCheck) (string, int) {
	if len(checks) == 0 {
		return "none", 0
	}
	failed, pending := 0, 0
	for _, check := range checks {
		conclusion := strings.ToUpper(check.Conclusion)
		state := strings.ToUpper(check.State)
		status := strings.ToUpper(check.Status)
		switch {
		case conclusion == "FAILURE" || conclusion == "CANCELLED" || conclusion == "TIMED_OUT" || conclusion == "ACTION_REQUIRED" || conclusion == "STARTUP_FAILURE" || conclusion == "STALE" || state == "FAILURE" || state == "ERROR":
			failed++
		case status != "COMPLETED" && conclusion == "" && state != "SUCCESS":
			pending++
		}
	}
	if failed > 0 {
		return "failed", failed
	}
	if pending > 0 {
		return "pending", pending
	}
	return "passed", len(checks)
}

func checkSummary(checks []gh.PRCheck) string {
	health, count := checkHealth(checks)
	switch health {
	case "failed":
		return stRedF.Render(fmt.Sprintf("CI %d failed", count))
	case "pending":
		return stAttention.Render(fmt.Sprintf("CI %d pending", count))
	case "passed":
		return stGreenF.Render(fmt.Sprintf("CI %d passed", count))
	default:
		return stMuted.Render("CI no checks")
	}
}

func previewMarkdown(text string, width, maxLines int) string {
	lines := strings.Split(md.Render(text, width), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], stMuted.Render("…"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) buildPRList() string {
	content, _ := m.buildPRListRows()
	return content
}

func (m Model) buildPRListRows() (string, int) {
	if len(m.openPRs) == 0 {
		if m.listRefreshing {
			return stMuted.Render("fetching open pull requests…"), 0
		}
		return stMuted.Render("(no pull requests in this view)"), 0
	}
	stacks := m.prStacks
	if len(stacks) == 0 {
		stacks = buildPRStacks(m.openPRs)
	}
	lines := make([]string, 0, len(m.openPRs)*3+len(stacks))
	selectedLine, openIndex := 0, 0
	for _, stack := range stacks {
		entries := stack.entries
		grouped := len(entries) > 1
		if grouped {
			collapsed := m.collapsedStacks[stack.id]
			arrow := "▾"
			if collapsed {
				arrow, entries = "▸", entries[:1]
			}
			header := stMuted.Render(arrow+" ") + stBold.Render(stack.title) + stMuted.Render(fmt.Sprintf(" · %d PRs · ", len(stack.entries))) + stackHealth(stack)
			lines = append(lines, ansi.Truncate(header, max(10, m.list.Width), "…"))
		}
		for i, entry := range entries {
			prefix := ""
			if grouped {
				marker := "├ "
				if i == len(entries)-1 {
					marker = "└ "
				}
				prefix = strings.Repeat("  ", entry.depth) + marker
			}
			selected := openIndex == m.prCursor
			if selected {
				selectedLine = len(lines)
			}
			lines = append(lines, m.renderPRRow(entry.pr, selected, prefix)...)
			openIndex++
		}
	}
	return strings.Join(lines, "\n"), selectedLine
}

func (m Model) renderPRRow(pr gh.PR, selected bool, prefix string) []string {
	state := "open"
	identifier := fmt.Sprintf("#%d", pr.Number)
	owner := " · @" + pr.Author.Login
	if pr.IsDraft {
		state = "draft"
	}
	if pr.Number == 0 {
		state, identifier, owner = "local", "Local PR", ""
	}
	stateStyle := stGreenF
	if state != "open" {
		stateStyle = stMuted
	}
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	line := selectionBar(selected) + stMuted.Render(prefix+identifier) + " " + stBold.Render(pr.Title)
	meta := "  " + indent + stateStyle.Render(state) + stMuted.Render(fmt.Sprintf(" · %s ← %s%s", pr.BaseRefName, pr.HeadRefName, owner))
	return []string{ansi.Truncate(line, max(10, m.list.Width), "…"), ansi.Truncate(meta, max(10, m.list.Width), "…"), ""}
}

func stackHealth(stack prStack) string {
	hasBlocked, hasFailed, hasPassed, hasPending := false, false, false, false
	for _, entry := range stack.entries {
		pr := entry.pr
		hasBlocked = hasBlocked || pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" || pr.MergeStateStatus == "BLOCKED"
		health, _ := checkHealth(pr.Checks)
		hasFailed = hasFailed || health == "failed"
		hasPending = hasPending || health == "pending"
		hasPassed = hasPassed || health == "passed"
	}
	if hasBlocked {
		return stRedF.Render("blocked")
	}
	if hasFailed {
		return stRedF.Render("CI failed")
	}
	if hasPending {
		return stAttention.Render("CI pending")
	}
	if hasPassed {
		return stGreenF.Render("CI passed")
	}
	return stMuted.Render("no checks")
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
		return stMuted.Render("(no conversation yet — try `live-pr note …`)"), 0
	}
	var lines []string
	selectedLine := 0
	for i, item := range items {
		selected := i == m.cursors[conversationTab]
		if selected {
			selectedLine = len(lines)
		}
		if item.pr != nil {
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

func (m Model) buildCommits() (string, int) {
	if len(m.commits) == 0 {
		return stMuted.Render("(no commits in " + m.base + "..HEAD)"), 0
	}
	lines := make([]string, 0, len(m.commits))
	for i, c := range m.commits {
		line := selectionBar(i == m.cursors[commitsTab]) + stAccent.Render(c.SHA) + " " + stFg.Render(c.Subject) + stMuted.Render(" · "+shortTS(c.Date))
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), m.cursors[commitsTab]
}

func (m Model) buildDetail() detailContent {
	if m.reviewSHA != "" {
		return m.commitDetail(m.reviewSHA)
	}
	if d := git.FileDiffRange(m.base, m.headRev); d != "" {
		return detailContent{key: "range:" + m.base + "..." + m.headRev, raw: d, renderable: true}
	}
	return detailContent{raw: stMuted.Render("(no changes in " + m.base + "..." + m.headRev + ")")}
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
		return renderStatus(m.status)
	}
	if m.pendingPRAction != noPRAction {
		action := "merge with a merge commit"
		switch m.pendingPRAction {
		case checkoutPR:
			action = "checkout its branch"
		case closePR:
			action = "close without merging"
		}
		return stAccent.Render(fmt.Sprintf("PR #%d: %s?", m.prActionNumber, action)) + stMuted.Render("  y confirm · n cancel")
	}
	if m.prActionRunning != noPRAction {
		action := "Merging"
		switch m.prActionRunning {
		case checkoutPR:
			action = "Checking out"
		case closePR:
			action = "Closing"
		}
		return stMuted.Render(fmt.Sprintf("%s PR #%d…", action, m.prActionNumber))
	}
	if m.notice != "" {
		return stGreenF.Render(m.notice) + "  " + m.help.View(m.keys)
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

func renderStatus(status string) string {
	for _, prefix := range []string{"loading ", "publishing ", "wait ", "select ", "local git data"} {
		if strings.HasPrefix(status, prefix) {
			return stAttention.Render(status)
		}
	}
	return stRedF.Render(status)
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
