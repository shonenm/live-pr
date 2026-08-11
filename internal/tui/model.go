// Package tui renders Conversation beside a branch- or commit-scoped review.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	bspinner "github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/debugtime"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prbody"
	"github.com/shonenm/live-pr/internal/prtemplate"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
)

type tab int

type screen int

type prView uint8

type prListState uint8

const (
	openPRListState prListState = iota
	closedPRListState
)

const (
	assignedView prView = iota
	reviewRequestedView
	allPRsView
	authoredView
	needsMeView
	closedPRsView
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
	Up, Down, PreviewUp, PreviewDown, Top, Bottom, PrevView, NextView, Filter, ToggleStack, Focus, FocusRight, FocusLeft, Commits, Select, Back, PRList, Browse, Refresh, Publish, Merge, Checkout, Close, AddComment, EditLocal, DeleteLocal, Help, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.PreviewUp, k.PreviewDown, k.Top, k.Bottom, k.PrevView, k.NextView, k.Filter, k.ToggleStack, k.Focus, k.FocusRight, k.Commits, k.Select, k.Back, k.PRList, k.Browse, k.Refresh, k.Publish, k.Merge, k.Checkout, k.Close, k.AddComment, k.EditLocal, k.DeleteLocal, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.PreviewUp, k.PreviewDown, k.Top, k.Bottom, k.PrevView, k.NextView, k.Filter, k.ToggleStack, k.Focus, k.FocusRight, k.Commits, k.Select, k.Back, k.PRList}, {k.AddComment, k.EditLocal, k.DeleteLocal, k.Browse, k.Refresh, k.Publish, k.Merge, k.Checkout, k.Close, k.Help, k.Quit}}
}

var keys = keyMap{
	Up:          key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:        key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	PreviewUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "page up")),
	PreviewDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "page down")),
	Top:         key.NewBinding(key.WithKeys("gg"), key.WithHelp("gg", "top")),
	Bottom:      key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
	PrevView:    key.NewBinding(key.WithKeys("["), key.WithHelp("[", "previous view")),
	NextView:    key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next view")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	ToggleStack: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "collapse stack")),
	Focus:       key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "toggle focus")),
	FocusRight:  key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "focus review")),
	FocusLeft:   key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "focus left")),
	Commits:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "commit/check")),
	Select:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "review commit")),
	Back:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "branch review")),
	PRList:      key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "PR list")),
	Browse:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open on GitHub")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Publish:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "publish PR")),
	Merge:       key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "merge PR")),
	Checkout:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "checkout PR")),
	Close:       key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "close PR")),
	AddComment:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add comment")),
	EditLocal:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit local")),
	DeleteLocal: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete comment")),
	Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:        key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

type prListRefreshed struct {
	generation uint64
	key        string
	appendPage bool
	page       gh.PRPage
	err        error
}

type currentBranchPRLoaded struct {
	pr  gh.PR
	err error
}

type prPreviewLoaded struct {
	generation uint64
	number     int
	pr         gh.PR
	err        error
}

type remoteLoaded struct {
	generation    uint64
	pr            gh.PR
	headRef       string
	comments      []gh.Comment
	activities    []gh.Activity
	refErr        error
	previewErr    error
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

type localEditMode uint8

const (
	noLocalEdit localEditMode = iota
	addLocalComment
	editLocalComment
	editLocalSummary
)

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

type prPageState struct {
	prs                             []gh.PR
	total                           int
	endCursor                       string
	hasNext, loaded, fresh, loading bool
}

type prRowCacheKey struct {
	number, width, additions, deletions, checkCount int
	prefix, state, title, author, base, head        string
	mergeable, mergeState, checkHealth, rollup      string
	draft, previewLoaded                            bool
}

type conversationItem struct {
	key      string
	ts       string
	summary  *string
	pr       *gh.PR
	event    *event.Event
	comment  *gh.Comment
	activity *gh.Activity
	prCommit *gh.PRCommit
}

// Model holds the living-PR view state.
type Model struct {
	screen                    screen
	title                     string
	root                      string
	repository                string
	currentBranch             string
	defaultBranch             string
	base, head                string
	diffBase, headRev         string
	reviewRange               string
	summary                   string
	events                    []event.Event
	conversationCache         []conversationItem
	conversationDirty         bool
	files                     []git.ChangedFile
	commits                   []git.Commit
	active                    tab
	cursors                   [tabCount]int
	reviewSHA                 string
	status                    string
	notice                    string
	githubStatus              string
	loadSpinner               bspinner.Model
	spinnerRunning            bool
	timelinePath              string
	cachePath                 string
	cache                     gh.Cache
	navigator                 gh.NavigatorCache
	navigatorPath             string
	allPRs                    []gh.PR
	filteredPRs               []gh.PR
	openPRs                   []gh.PR
	viewerLogin               string
	prPreviewLoading          map[int]bool
	prPreviewLoaded           map[int]bool
	prView                    prView
	prListState               prListState
	prPages                   map[string]prPageState
	activePRPage              string
	viewCounts                [prViewCount]int
	viewCountKnown            [prViewCount]bool
	viewCountsValid           bool
	filterQuery               string
	filterBeforeEdit          string
	filterSelectionBeforeEdit int
	filterEditing             bool
	prStacks                  []prStack
	prRowCache                map[prRowCacheKey][]string
	collapsedStacks           map[string]bool
	prCursor                  int
	localAvailable            bool
	localTitle                string
	localStats                git.ChangeStats
	localCommitCount          int
	workingTreeDirty          bool
	autoOpenCurrent           bool
	refreshing                bool
	listRefreshing            bool
	publishing                bool
	localEditMode             localEditMode
	localEditor               textarea.Model
	localEditTarget           string
	localEditError            string
	localDeleteTarget         string
	localDeleteTitle          string
	pendingPRAction           prAction
	prActionRunning           prAction
	prActionNumber            int
	pendingG                  bool
	prActionPR                gh.PR
	prListGeneration          uint64
	remote                    bool
	targetGeneration          uint64

	diffDisplay       string
	diffCommand       string
	diffCommitCommand string
	diffTerminal      *embeddedterm.Terminal
	focusDiff         bool
	focusExplorer     bool
	fileCursor        int
	detailKey         string
	rawDetailCache    map[string]string
	diffCache         map[string]string
	diffPending       map[string]bool
	checkedFiles      map[string]bool

	list     viewport.Model
	explorer viewport.Model
	detail   viewport.Model
	help     help.Model
	keys     keyMap
	w, h     int
	ready    bool
}

// New builds a navigator-aware model without creating branch state unless the
// current checkout is routed to local detail.
func New() (Model, error) {
	if done := debugtime.Start("tui startup"); done != nil {
		defer done()
	}
	root, err := git.RepoRoot()
	if err != nil {
		return Model{}, err
	}
	branch, err := git.CurrentBranch()
	if err != nil {
		return Model{}, err
	}
	if err := store.MigrateLegacy(root); err != nil {
		return Model{}, fmt.Errorf("migrate live-pr state: %w", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		return Model{}, err
	}
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
	hasChanges, _ := git.HasChanges(base, "HEAD")
	currentPR := cache.PR
	if currentPR != nil && !isCurrentPR(*currentPR, branch) && !cache.ExplicitCheckout {
		currentPR = nil
		cache = gh.NewCache(branch)
	}
	if currentPR == nil {
		currentPR = currentBranchPR(navigator.PRs, branch)
	}
	defaultBranch := strings.TrimPrefix(defaultRef, "origin/")
	localEligible := branch != "HEAD" && branch != defaultBranch
	localDetail := shouldOpenLocal(branch, defaultBranch, currentPR != nil, st.HasData(), hasChanges)
	initialView, initialState := assignedView, openPRListState
	if currentPR != nil && matchesListState(*currentPR, closedPRListState) {
		initialView, initialState = closedPRsView, closedPRListState
	}

	m := Model{
		screen:            prListScreen,
		root:              root,
		repository:        navigator.Repository,
		currentBranch:     branch,
		defaultBranch:     defaultBranch,
		base:              base,
		head:              branch,
		headRev:           "HEAD",
		status:            status,
		loadSpinner:       newLoadSpinner(),
		spinnerRunning:    true,
		navigator:         navigator,
		navigatorPath:     navigatorPath,
		openPRs:           navigator.PRs,
		viewerLogin:       navigator.ViewerLogin,
		prView:            initialView,
		prListState:       initialState,
		localAvailable:    localEligible && currentPR == nil,
		localTitle:        branch,
		autoOpenCurrent:   localEligible,
		listRefreshing:    true,
		prListGeneration:  1,
		conversationDirty: true,
		diffDisplay:       cfg.Diff.Display,
		diffCommand:       cfg.Diff.Command,
		diffCommitCommand: cfg.CommitReviewCommand(),
		rawDetailCache:    map[string]string{},
		diffCache:         map[string]string{},
		diffPending:       map[string]bool{},
		prPreviewLoading:  map[int]bool{},
		prPreviewLoaded:   map[int]bool{},
		prPages:           map[string]prPageState{},
		collapsedStacks:   map[string]bool{},
		prRowCache:        map[prRowCacheKey][]string{},
		help:              newHelp(),
		keys:              keys,
	}
	if localDetail {
		if err := m.loadLocal(st, cache, currentPR); err != nil {
			return Model{}, err
		}
	}
	m.seedPRPages()
	m.activePRPage = prPageKey(m.prView, m.prListState, m.filterQuery)
	page := m.prPages[m.activePRPage]
	page.loading = true
	m.prPages[m.activePRPage] = page
	m.applyPRFilters(0)
	return m, nil
}

func isCurrentPR(pr gh.PR, branch string) bool {
	return !pr.IsCrossRepository && (pr.HeadRefName == "" || pr.HeadRefName == branch)
}

func currentBranchPR(prs []gh.PR, branch string) *gh.PR {
	for _, state := range []prListState{openPRListState, closedPRListState} {
		for i := range prs {
			if matchesListState(prs[i], state) && isCurrentPR(prs[i], branch) {
				return &prs[i]
			}
		}
	}
	return nil
}

func (m Model) isCurrentTargetPR(pr gh.PR) bool {
	return isCurrentPR(pr, m.currentBranch) || (!m.remote && m.cache.ExplicitCheckout && m.cache.PR != nil && m.cache.PR.Number == pr.Number)
}

func (m Model) currentPRNumber() int {
	if m.cache.PR != nil {
		return m.cache.PR.Number
	}
	return 0
}

func (m Model) canMergeCurrentPR() bool {
	if m.cache.PR == nil || m.cache.PR.Number <= 0 || m.cache.PR.HeadRefOID == "" {
		return false
	}
	return strings.EqualFold(m.cache.PR.State, "OPEN")
}

func shouldOpenLocal(branch, defaultBranch string, hasPR, hasData, hasChanges bool) bool {
	return branch != "HEAD" && branch != defaultBranch && (hasPR || hasData || hasChanges)
}

func localReviewBase(base string, pr *gh.PR) string {
	if pr != nil && pr.BaseRefOID != "" {
		if mergeBase, err := git.MergeBase(pr.BaseRefOID, "HEAD"); err == nil {
			return mergeBase
		}
	}
	if mergeBase, err := git.MergeBase(base, "HEAD"); err == nil {
		return mergeBase
	}
	return base
}

func remoteReviewBase(pr gh.PR) string {
	if pr.BaseRefOID != "" {
		return pr.BaseRefOID
	}
	return git.ResolveBase(pr.BaseRefName)
}

func (m *Model) loadLocal(st *store.Store, cache gh.Cache, hintedPR *gh.PR) error {
	if done := debugtime.Start("tui local hydration"); done != nil {
		defer done()
	}
	m.targetGeneration++
	m.resetDetailCaches()
	if err := st.Ensure(); err != nil {
		return err
	}
	if err := prtemplate.Seed(st); err != nil {
		return err
	}
	if cache.PR == nil && hintedPR != nil && hintedPR.Number > 0 {
		pr := *hintedPR
		cache.PR = &pr
	}
	base := git.ResolveBase(cache.Base(git.DefaultBase()))
	diffBase := localReviewBase(base, cache.PR)
	_, _ = timeline.SyncCommits(st.Timeline(), diffBase)
	events, err := event.Load(st.Timeline())
	if err != nil {
		return err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	commits, commitErr := git.Commits(diffBase)
	files, fileErr := git.ChangedFiles(diffBase)
	stats, _ := git.DiffStats(diffBase, "HEAD")
	dirty, dirtyErr := git.HasUncommittedChanges()
	if commitErr != nil || fileErr != nil || dirtyErr != nil {
		m.status = "local git data is incomplete"
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	prURL := ""
	if cache.PR != nil {
		prURL = cache.PR.URL
	}
	conclusion, _ := os.ReadFile(st.Conclusion())
	m.screen = detailScreen
	m.remote = false
	m.summary = string(conclusion)
	m.title = prbody.Title(m.summary, st.Branch)
	m.localAvailable, m.localTitle = cache.PR == nil, m.title
	m.localStats, m.localCommitCount, m.workingTreeDirty = stats, len(commits), dirty
	m.base, m.diffBase, m.head, m.headRev, m.reviewRange = base, diffBase, st.Branch, "HEAD", diffBase
	m.events, m.files, m.commits = events, files, commits
	m.timelinePath, m.cachePath, m.cache = st.Timeline(), st.GitHubCache(), cache
	m.invalidateConversation()
	m.githubStatus = "Local only · checking for PR…"
	if cache.PR != nil {
		m.githubStatus = "GitHub: cached · refreshing…"
	}
	m.refreshing, m.publishing = true, false
	m.diffTerminal = embeddedterm.New(m.diffCommand, m.root, embeddedterm.Environment(m.reviewRange, diffBase, st.Branch, "HEAD", prURL, ""))
	m.focusDiff, m.focusExplorer, m.fileCursor, m.active, m.reviewSHA = false, false, 0, conversationTab, ""
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
	if len(m.loadSpinner.Spinner.Frames) == 0 {
		m.loadSpinner = newLoadSpinner()
	}
	m.spinnerRunning = true
	cmds := []tea.Cmd{fetchPRList(m.prListGeneration, m.activePRPage, prViewSearch(m.prView, m.prListState, m.filterQuery), "", false), m.loadSpinner.Tick}
	if m.screen == prListScreen && m.localAvailable && m.autoOpenCurrent {
		cmds = append(cmds, fetchCurrentBranchPR(m.currentBranch))
	}
	if m.screen == detailScreen && !m.remote && m.cachePath != "" {
		cmds = append(cmds, fetchGitHub(m.head, m.currentPRNumber(), m.targetGeneration))
	}
	if m.diffTerminal != nil {
		cmds = append(cmds, m.diffTerminal.Init())
	}
	return tea.Batch(cmds...)
}

func (m Model) isLoading() bool {
	return m.refreshing || m.listRefreshing || m.publishing || m.prActionRunning != noPRAction || len(m.prPreviewLoading) > 0 || len(m.diffPending) > 0
}

func (m *Model) startSpinner() tea.Cmd {
	if !m.isLoading() {
		return nil
	}
	if len(m.loadSpinner.Spinner.Frames) == 0 {
		m.loadSpinner = newLoadSpinner()
	}
	if m.spinnerRunning {
		return nil
	}
	m.spinnerRunning = true
	return m.loadSpinner.Tick
}

func (m Model) busyStatus(text string) string {
	if text == "" {
		return m.loadSpinner.View()
	}
	return m.loadSpinner.View() + " " + renderStatus(text)
}

func (m *Model) close() {
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
}

func (m *Model) advanceAsyncGenerations(previous Model) {
	m.targetGeneration = previous.targetGeneration + 1
	m.prListGeneration = previous.prListGeneration + 1
	m.resetDetailCaches()
}

func (m *Model) invalidateConversation() {
	m.conversationDirty = true
}

func fetchPRList(generation uint64, key, query, cursor string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		page, err := gh.New().SearchPRs(query, cursor)
		return prListRefreshed{generation: generation, key: key, appendPage: appendPage, page: page, err: err}
	}
}

func fetchCurrentBranchPR(head string) tea.Cmd {
	return func() tea.Msg {
		pr, err := gh.New().FindForHead(head)
		return currentBranchPRLoaded{pr: pr, err: err}
	}
}

func fetchPRPreview(number int, generation uint64) tea.Cmd {
	return func() tea.Msg {
		pr, err := gh.New().FindPreview(number)
		return prPreviewLoaded{generation: generation, number: number, pr: pr, err: err}
	}
}

func fetchGitHub(head string, number int, generation uint64) tea.Cmd {
	return func() tea.Msg {
		client := gh.New()
		if number == 0 {
			pr, err := client.FindForHead(head)
			if err != nil {
				return githubRefreshed{generation: generation, err: err}
			}
			number = pr.Number
		}
		detail := client.LoadPRDetail(number)
		return githubRefreshed{generation: generation, pr: detail.PR, comments: detail.Comments, activities: detail.Activities, err: detail.PreviewErr, commentsErr: detail.CommentsErr, activitiesErr: detail.ActivitiesErr}
	}
}

func fetchRemotePR(pr gh.PR, generation uint64) tea.Cmd {
	return func() tea.Msg {
		client := gh.New()
		var headRef string
		var comments []gh.Comment
		var activities []gh.Activity
		var refErr, previewErr, commentsErr, activitiesErr error
		number, base, headOID := pr.Number, pr.BaseRefName, pr.HeadRefOID
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			headRef, refErr = git.FetchPull(number, base, headOID)
		}()
		go func() {
			defer wg.Done()
			detail := client.LoadPRDetail(number)
			comments, activities = detail.Comments, detail.Activities
			previewErr, commentsErr, activitiesErr = detail.PreviewErr, detail.CommentsErr, detail.ActivitiesErr
			if previewErr == nil {
				pr = detail.PR
			}
		}()
		wg.Wait()
		return remoteLoaded{generation: generation, pr: pr, headRef: headRef, comments: comments, activities: activities, refErr: refErr, previewErr: previewErr, commentsErr: commentsErr, activitiesErr: activitiesErr}
	}
}

func (m *Model) openRemote(pr gh.PR) tea.Cmd {
	m.targetGeneration++
	m.resetDetailCaches()
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	m.screen, m.remote = detailScreen, true
	m.title = pr.Title
	m.base = git.ResolveBase(pr.BaseRefName)
	m.diffBase = remoteReviewBase(pr)
	m.head = pr.HeadRefName
	m.headRev = fmt.Sprintf("refs/live-pr/pulls/%d/head", pr.Number)
	m.reviewRange = m.diffBase + "..." + m.headRev
	m.events, m.commits, m.files = nil, nil, nil
	m.timelinePath, m.cachePath = "", ""
	m.cache = gh.NewCache(pr.HeadRefName)
	m.cache.PR = &pr
	if snapshot, ok := m.navigator.Snapshot(pr.Number); ok {
		m.cache.Comments = snapshot.Comments
		m.cache.Activities = snapshot.Activities
		m.cache.FetchedAt = snapshot.FetchedAt
	}
	m.invalidateConversation()
	m.reviewSHA, m.active, m.focusDiff, m.focusExplorer, m.fileCursor = "", conversationTab, false, false, 0
	m.diffTerminal = nil
	m.refreshing, m.publishing = true, false
	m.status = "loading PR refs…"
	m.githubStatus = "GitHub: cached · refreshing selected PR…"
	m.layout()
	return tea.Batch(fetchRemotePR(pr, m.targetGeneration), m.sync(), m.startSpinner())
}

const (
	footerLines     = 1
	paneChromeW     = 4 // border + one space padding per side
	paneChromeH     = 2 // top and bottom border
	dividerW        = 3 // padded rule between the file list and the diff
	listRatio       = 52
	reviewListRatio = 38
	prListPaneRatio = 45
)

func (m *Model) layout() {
	if m.screen == prListScreen {
		bodyH := max(3, m.h-m.headerHeight()-footerLines-paneChromeH)
		listPaneW := max(24, m.w*prListPaneRatio/100)
		if m.w-listPaneW < 20 {
			listPaneW = max(12, m.w-20)
		}
		listW := max(4, listPaneW-paneChromeW)
		detailW := max(4, m.w-listPaneW-paneChromeW)
		if !m.ready {
			m.list = viewport.New(listW, bodyH)
			m.detail = viewport.New(detailW, bodyH)
			m.ready = true
		} else {
			if m.list.Width != listW {
				clear(m.prRowCache)
			}
			m.list.Width, m.list.Height = listW, bodyH
			m.detail.Width, m.detail.Height = detailW, bodyH
		}
		return
	}
	ratio := listRatio
	if m.diffTerminal != nil && m.diffTerminal.Available() {
		ratio = reviewListRatio
	}
	leftPaneW := max(24, m.w*ratio/100)
	rightPaneW := m.w - leftPaneW
	if rightPaneW < 14 {
		rightPaneW = 14
		leftPaneW = max(8, m.w-14)
	}
	listW := max(4, leftPaneW-paneChromeW)
	rightW := max(4, rightPaneW-paneChromeW)
	bodyH := m.h - m.headerHeight() - footerLines - paneChromeH
	if bodyH < 3 {
		bodyH = 3
	}
	// The file list and the diff share one frame, split by an inner rule, so
	// the review side reads as a single region instead of two boxes.
	explorerW, detailW := 1, rightW
	if m.fileExplorerMode() {
		explorerW = max(14, rightW/3)
		if rightW-explorerW-dividerW < 20 {
			explorerW = max(8, rightW-dividerW-20)
		}
		detailW = max(4, rightW-explorerW-dividerW)
	}
	if !m.ready {
		m.list = viewport.New(listW, bodyH)
		m.explorer = viewport.New(explorerW, bodyH)
		m.detail = viewport.New(detailW, bodyH)
		m.ready = true
	} else {
		m.list.Width, m.list.Height = listW, bodyH
		m.explorer.Width, m.explorer.Height = explorerW, bodyH
		m.detail.Width, m.detail.Height = detailW, bodyH
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Resize(detailW, bodyH)
	}
}

func (m *Model) handleVimNavigation(msg tea.KeyMsg) (bool, tea.Cmd) {
	if msg.String() == "g" {
		if m.pendingG {
			m.pendingG = false
			return true, m.moveCursorTo(0)
		}
		m.pendingG = true
		return true, nil
	}
	m.pendingG = false
	if key.Matches(msg, m.keys.Bottom) {
		return true, m.moveCursorTo(m.navigationLength() - 1)
	}
	if key.Matches(msg, m.keys.PreviewUp) {
		return true, m.moveCursorBy(-m.navigationPage())
	}
	if key.Matches(msg, m.keys.PreviewDown) {
		return true, m.moveCursorBy(m.navigationPage())
	}
	return false, nil
}

func (m *Model) navigationLength() int {
	if m.screen == prListScreen {
		return len(m.openPRs)
	}
	if m.focusExplorer {
		return len(m.files)
	}
	return m.activeLen()
}

func (m Model) navigationPage() int {
	height := m.detail.Height
	if m.screen == prListScreen {
		height = m.list.Height / 3
	}
	return max(1, height/2)
}

func (m *Model) moveCursorBy(delta int) tea.Cmd {
	return m.moveCursorTo(m.navigationCursor() + delta)
}

func (m *Model) moveCursorTo(index int) tea.Cmd {
	length := m.navigationLength()
	if length == 0 {
		return nil
	}
	if index < 0 {
		index = 0
	}
	if index >= length {
		index = length - 1
	}
	if m.screen == prListScreen {
		m.prCursor = index
		cmd := m.sync()
		if index == length-1 {
			return tea.Batch(cmd, m.requestPRPage(false))
		}
		return cmd
	} else if m.focusExplorer {
		m.fileCursor = index
	} else {
		m.cursors[m.active] = index
	}
	return m.sync()
}

func (m Model) navigationCursor() int {
	if m.screen == prListScreen {
		return m.prCursor
	}
	if m.focusExplorer {
		return m.fileCursor
	}
	return m.cursors[m.active]
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

func (m Model) selectedCommitSHA() string {
	if i := m.cursors[commitsTab]; m.active == commitsTab && i >= 0 && i < len(m.commits) {
		return m.commits[i].SHA
	}
	return ""
}

func (m *Model) sync() tea.Cmd {
	if done := debugtime.Start("tui sync"); done != nil {
		defer done()
	}
	if !m.ready {
		return nil
	}
	start := m.startSpinner()
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
		m.keys.Merge.SetEnabled(m.prListState == openPRListState && pr != nil && pr.Number > 0 && pr.HeadRefOID != "" && m.prActionRunning == noPRAction)
		m.keys.Checkout.SetEnabled(pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) && m.prActionRunning == noPRAction)
		m.keys.Close.SetEnabled(m.prListState == openPRListState && pr != nil && pr.Number > 0 && m.prActionRunning == noPRAction)
		content, selectedLine := m.buildPRListRows()
		m.list.SetContent(content)
		m.detail.SetContent(m.buildPRPreview())
		m.detail.GotoTop()
		keepLineVisible(&m.list, selectedLine)
		preview := m.ensureSelectedPRPreview()
		return tea.Batch(start, preview, m.startSpinner())
	}
	m.keys.PreviewUp.SetEnabled(true)
	m.keys.PreviewDown.SetEnabled(true)
	m.keys.PrevView.SetEnabled(false)
	m.keys.NextView.SetEnabled(false)
	m.keys.Filter.SetEnabled(false)
	m.keys.ToggleStack.SetEnabled(false)
	m.keys.Focus.SetEnabled(true)
	m.keys.FocusRight.SetEnabled(true)
	m.keys.Commits.SetEnabled(m.fileExplorerMode() || m.active == conversationTab)
	m.keys.PRList.SetEnabled(true)
	m.keys.Select.SetEnabled(m.active == commitsTab)
	m.keys.Back.SetEnabled(m.active == commitsTab)
	m.keys.Browse.SetEnabled(m.selectedBrowseURL() != "")
	m.keys.Publish.SetEnabled(!m.remote)
	m.keys.Merge.SetEnabled(m.canMergeCurrentPR() && m.prActionRunning == noPRAction)
	m.keys.Checkout.SetEnabled(false)
	m.keys.Close.SetEnabled(false)
	content, selectedLine := m.buildList()
	m.list.SetContent(content)
	keepLineVisible(&m.list, selectedLine)
	if m.fileExplorerMode() {
		explorer, selectedFileLine := m.buildFileExplorer()
		m.explorer.SetContent(explorer)
		keepLineVisible(&m.explorer, selectedFileLine)
	}

	detailCmd := m.syncDetail(m.loadDetail())
	return tea.Batch(start, detailCmd, m.startSpinner())
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	var view string
	if m.screen == prListScreen {
		listTitle := fmt.Sprintf("%s · %d", m.prView, len(m.filteredPRs))
		previewTitle := "Preview"
		if pr := m.selectedPR(); pr != nil {
			if pr.Number > 0 {
				previewTitle = fmt.Sprintf("Preview · #%d", pr.Number)
			} else {
				previewTitle = "Preview · local"
			}
		}
		listPane := renderPane(listTitle, m.list.View(), m.list.Width+paneChromeW, m.list.Height+paneChromeH, true)
		previewPane := renderPane(previewTitle, m.detail.View(), m.detail.Width+paneChromeW, m.detail.Height+paneChromeH, false)
		body := lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane)
		view = lipgloss.JoinVertical(lipgloss.Left, m.renderPRListHeader(), body, m.renderFooter())
	} else {
		leftTitle := "Conversation"
		if m.active == commitsTab {
			leftTitle = fmt.Sprintf("Commits · %d", len(m.commits))
		}
		left := renderPane(leftTitle, m.list.View(), m.list.Width+paneChromeW, m.list.Height+paneChromeH, !m.focusDiff && !m.focusExplorer)
		body := lipgloss.JoinHorizontal(lipgloss.Top, left, m.renderReviewPane())
		view = lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
	}
	if m.localEditMode != noLocalEdit {
		return overlayPopup(view, m.renderLocalEditorPopup(), m.w)
	}
	if m.localDeleteTarget != "" {
		return overlayPopup(view, m.renderLocalDeletePopup(), m.w)
	}
	if m.pendingPRAction != noPRAction || m.prActionRunning != noPRAction {
		return overlayPopup(view, m.renderActionPopup(), m.w)
	}
	return view
}

// headerHeight is the wordmark's height on both screens: the header text is
// never taller, so the metadata row costs no extra space.
func (m Model) headerHeight() int { return logoHeight }

// withLogo anchors the wordmark at the far left of a header block and pads the
// text to the wordmark's height. Narrow terminals drop the wordmark but keep
// the height, so the layout never jumps.
func (m Model) withLogo(text string) string {
	width := m.w
	if m.w >= logoWidth+40 {
		width = m.w - logoWidth
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(1, width), "…")
	}
	for len(lines) < logoHeight {
		lines = append(lines, "")
	}
	block := strings.Join(lines, "\n")
	if width == m.w {
		return block
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, renderLogo(), block)
}

func (m Model) detailStats() git.ChangeStats {
	if !m.remote {
		stats := m.localStats
		if stats.Files == 0 && len(m.files) > 0 {
			stats.Files = len(m.files)
		}
		return stats
	}
	if m.cache.PR != nil {
		return git.ChangeStats{Files: m.cache.PR.ChangedFiles, Additions: m.cache.PR.Additions, Deletions: m.cache.PR.Deletions}
	}
	return git.ChangeStats{Files: len(m.files)}
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
	stats := m.detailStats()
	scope := fmt.Sprintf("%d files", stats.Files) + " " + stGreenF.Render(fmt.Sprintf("+%d", stats.Additions)) + " " + stRedF.Render(fmt.Sprintf("-%d", stats.Deletions))
	if m.reviewSHA != "" {
		scope = "commit " + m.reviewSHA
	}
	dirty := ""
	if !m.remote && m.workingTreeDirty {
		dirty = "   " + stAttention.Render("● uncommitted changes")
	}
	l2 := stMuted.Render("⎇ ") + stBold.Render(m.base) + stMuted.Render(" ← ") + stFg.Render(m.head) + stMuted.Render("   · ") + scope + dirty
	lines := []string{l1, l2}
	if m.cache.PR != nil {
		lines = append(lines, m.renderPRMeta(*m.cache.PR))
	}
	return m.withLogo(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m Model) footerMode() string {
	if m.screen == prListScreen {
		return "PRS"
	}
	switch {
	case m.focusDiff:
		return "REVIEW"
	case m.focusExplorer:
		return "FILES"
	case m.active == commitsTab:
		return "COMMITS"
	default:
		return "CONV"
	}
}

func (m Model) renderFooter() string {
	return footerSegment(m.footerMode()) + " " + m.footerContent()
}

func (m Model) footerContent() string {
	if m.status != "" {
		if m.isLoading() {
			return m.busyStatus(m.status)
		}
		return renderStatus(m.status)
	}
	if m.pendingPRAction != noPRAction {
		return m.help.View(m.keys)
	}
	if m.prActionRunning != noPRAction {
		return m.busyStatus("")
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
		if m.isLoading() {
			return m.busyStatus(m.githubStatus) + "  " + m.help.View(m.keys)
		}
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

func keepLineVisible(v *viewport.Model, line int) {
	if line < v.YOffset {
		v.SetYOffset(line)
		return
	}
	if bottom := v.YOffset + v.Height - 1; line > bottom {
		v.SetYOffset(line - v.Height + 1)
	}
}

func selectionBar(selected bool) string {
	if selected {
		return stAccent.Render("▌") + " "
	}
	return "  "
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
