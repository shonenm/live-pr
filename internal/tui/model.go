// Package tui renders Conversation beside a branch- or commit-scoped review.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	bspinner "github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/debugtime"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prbody"
	"github.com/shonenm/live-pr/internal/store"
)

type tab int

type screen int

// prView indexes Model.views, the config-defined PR list tabs.
type prView int

type prListState uint8

const (
	openPRListState prListState = iota
	closedPRListState
)

const (
	conversationTab tab = iota
	commitsTab
	conflictsTab
	checksTab
	tabCount
)

const (
	detailScreen screen = iota
	prListScreen
)

type keyMap struct {
	Up, Down, PreviewUp, PreviewDown, Top, Bottom, PrevView, NextView, Filter, ToggleStack, Focus, FocusRight, FocusLeft, Commits, Conflicts, Checks, Select, Back, PRList, Browse, Refresh, Publish, Merge, Checkout, Close, Status, AddComment, InlineReview, EditLocal, DeleteLocal, Review, ManageViews, CopyURL, Help, Quit key.Binding
}

// helpGroups is the single source of help ordering; ShortHelp flattens it and
// FullHelp shows it as-is, so a binding can no longer drop out of one of the
// three hand-maintained lists. FocusLeft is deliberately absent: q quits even
// while the review is focused (Quit wins in the key handler), so the binding
// only exists for reservedReviewKey and its help text would lie.
func (k keyMap) helpGroups() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PreviewUp, k.PreviewDown, k.Top, k.Bottom, k.PrevView, k.NextView, k.Filter, k.ToggleStack, k.Focus, k.FocusRight, k.Commits, k.Conflicts, k.Checks, k.Select, k.Back, k.PRList},
		{k.AddComment, k.InlineReview, k.EditLocal, k.DeleteLocal, k.Review, k.Status, k.Browse, k.CopyURL, k.Refresh, k.Publish, k.Merge, k.Checkout, k.Close, k.ManageViews, k.Help, k.Quit},
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	var all []key.Binding
	for _, group := range k.helpGroups() {
		all = append(all, group...)
	}
	return all
}

func (k keyMap) FullHelp() [][]key.Binding { return k.helpGroups() }

var keys = keyMap{
	Up:           key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:         key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	PreviewUp:    key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "page up")),
	PreviewDown:  key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "page down")),
	Top:          key.NewBinding(key.WithKeys("gg"), key.WithHelp("gg", "top")),
	Bottom:       key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
	PrevView:     key.NewBinding(key.WithKeys("[", "h"), key.WithHelp("h/[", "previous view")),
	NextView:     key.NewBinding(key.WithKeys("]", "l"), key.WithHelp("l/]", "next view")),
	Filter:       key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	ToggleStack:  key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "collapse stack")),
	Focus:        key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "move focus")),
	FocusRight:   key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "focus review")),
	FocusLeft:    key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "focus left")),
	Commits:      key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "commits")),
	Conflicts:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "conflicts")),
	Checks:       key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "CI checks")),
	Select:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "review commit")),
	Back:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "conversation")),
	PRList:       key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "PR list")),
	Browse:       key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open on GitHub")),
	Refresh:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Publish:      key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "publish PR")),
	Merge:        key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "merge PR")),
	Checkout:     key.NewBinding(key.WithKeys("c", "C"), key.WithHelp("c/C", "checkout PR")),
	Close:        key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "close PR")),
	Status:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "PR status")),
	AddComment:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "comment")),
	InlineReview: key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "inline review comment")),
	EditLocal:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit local")),
	DeleteLocal:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete comment")),
	Review:       key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "review (verdict+body)")),
	CopyURL:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy URL")),
	ManageViews:  key.NewBinding(key.WithKeys("V"), key.WithHelp("V", "manage views")),
	Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:         key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

type prAction uint8

type localEditMode uint8

const (
	noLocalEdit localEditMode = iota
	addLocalComment
	editLocalComment
	editLocalSummary
	editReviewBody
	addInlineReviewComment
	addRemoteComment
	editRemoteComment
)

const (
	noPRAction prAction = iota
	mergePR
	checkoutPR
	closePR
)

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
	draft, previewLoaded, current                   bool
}

// listAnchor records which selected line the left pane was last scrolled to,
// so a background refresh can tell "the selection moved" from "the reader
// scrolled away from it".
type listAnchor struct {
	tab    tab
	line   int
	pinned bool
}

type conversationItem struct {
	key           string
	ts            string
	summary       *string
	pr            *gh.PR
	event         *event.Event
	comment       *gh.Comment
	activity      *gh.Activity
	prCommit      *gh.PRCommit
	review        *gh.Review
	reviewComment *gh.ReviewThreadComment
}

type conversationItemKind uint8

const (
	itemUnknown conversationItemKind = iota
	itemSummary
	itemPRDescription
	itemComment
	itemReview
	itemReviewComment
	itemActivity
	itemPRCommit
	itemEvent
)

// kind classifies which pointer of the hand-rolled sum type is populated, so
// consumers switch once instead of chaining nil checks — and itemEvent is
// only reported when event is actually set, closing the nil-deref tail the
// old else-branches carried.
func (it conversationItem) kind() conversationItemKind {
	switch {
	case it.summary != nil:
		return itemSummary
	case it.pr != nil:
		return itemPRDescription
	case it.comment != nil:
		return itemComment
	case it.review != nil:
		return itemReview
	case it.reviewComment != nil:
		return itemReviewComment
	case it.activity != nil:
		return itemActivity
	case it.prCommit != nil:
		return itemPRCommit
	case it.event != nil:
		return itemEvent
	default:
		return itemUnknown
	}
}

// compactActivity items render as one-liners and pack together without blank
// separators.
func (it conversationItem) compactActivity() bool {
	kind := it.kind()
	return kind == itemActivity || kind == itemPRCommit
}

// githubClient is the slice of gh.Client the TUI commands use, injectable in
// tests.
type githubClient interface {
	SearchPRs(query, cursor string) (gh.PRPage, error)
	FindForHead(head string) (gh.PR, error)
	FindPreview(number int) (gh.PR, error)
	FindChecks(number int) (gh.PR, error)
	LoadPRDetail(number int, prev gh.PRDetail) gh.PRDetail
	Merge(number int, headOID string, method gh.MergeMethod) error
	Checkout(number int) error
	Close(number int) error
	SetStatus(pr gh.PR, target string) error
	PostIssueComment(number int, body string) error
	EditIssueComment(id int64, body string) error
	DeleteIssueComment(id int64) error
	UpdateBody(number int, bodyFile string) error
	SubmitReview(draft gh.ReviewDraft, event gh.ReviewEvent) error
}

// Model holds the living-PR view state.
type Model struct {
	client            githubClient
	screen            screen
	root              string
	repository        string
	currentBranch     string
	defaultBranch     string
	avatarColors      map[string]string
	status            string
	notice            string
	githubStatus      string
	loadSpinner       bspinner.Model
	spinnerRunning    bool
	timelinePath      string
	cachePath         string
	cache             gh.Cache
	navigator         gh.NavigatorCache
	navigatorPRIndex  map[int]int
	navigatorPath     string
	viewerLogin       string
	prList            prListModel
	detailView        detailModel
	detailOrigin      prView
	detailOriginSet   bool
	views             []config.View
	localAvailable    bool
	localTitle        string
	localStats        git.ChangeStats
	localCommitCount  int
	workingTreeDirty  bool
	autoOpenCurrent   bool
	refreshing        bool
	ciPollFailures    int
	publishing        bool
	overlay           overlay // open modal popup; nil when none
	localEditor       textarea.Model
	remoteCommentBusy bool
	reviewDraft       gh.ReviewDraft
	reviewDraftPath   string
	reviewSubmitting  bool
	pendingPRAction   prAction
	prActionRunning   prAction
	prActionNumber    int
	mergeMethodCursor int
	pendingG          bool
	prActionPR        gh.PR
	remote            bool
	targetGeneration  uint64

	diffDisplay       string
	diffCommand       string
	diffCommitCommand string
	diffSplitRatio    int
	diffMinPaneWidth  int
	diffTerminal      *embeddedterm.Terminal

	list     viewport.Model
	explorer viewport.Model
	detail   viewport.Model
	help     help.Model
	keys     keyMap
	w, h     int
	ready    bool
	version  string
}

// New builds a navigator-aware model without creating branch state unless the
// current checkout is routed to local detail.
func New(version ...string) (Model, error) {
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
	cache, cacheErr := st.LoadGitHubCache()
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
	views := config.NormalizeViews(cfg.Views)
	initialView, initialState := prView(0), openPRListState
	if currentPR != nil && matchesListState(*currentPR, closedPRListState) {
		// Open on a tab that can actually show the branch's closed PR.
		for i, view := range views {
			if view.Closed() {
				initialView, initialState = prView(i), closedPRListState
				break
			}
		}
	}

	m := Model{
		client:          gh.New(),
		screen:          prListScreen,
		version:         first(version),
		root:            root,
		repository:      navigator.Repository,
		currentBranch:   branch,
		defaultBranch:   defaultBranch,
		status:          status,
		loadSpinner:     newLoadSpinner(),
		spinnerRunning:  true,
		navigator:       navigator,
		navigatorPath:   navigatorPath,
		viewerLogin:     navigator.ViewerLogin,
		views:           views,
		localAvailable:  localEligible && currentPR == nil,
		localTitle:      branch,
		autoOpenCurrent: localEligible,
		detailView: detailModel{
			base:              base,
			head:              branch,
			headRev:           "HEAD",
			conversationDirty: true,
			rawCache:          map[string]string{},
			diffCache:         map[string]string{},
			richBodies:        map[string]string{},
			diffPending:       map[string]bool{},
		},
		prList: prListModel{
			open:            navigator.PRs,
			view:            initialView,
			state:           initialState,
			refreshing:      true,
			generation:      1,
			previewLoading:  map[int]bool{},
			previewLoaded:   map[int]bool{},
			pages:           map[string]prPageState{},
			collapsedStacks: map[string]bool{},
			rowCache:        map[prRowCacheKey][]string{},
		},
		diffDisplay:       cfg.Diff.Display,
		diffCommand:       cfg.Diff.Command,
		diffCommitCommand: cfg.CommitReviewCommand(),
		diffSplitRatio:    cfg.Diff.SplitRatio,
		diffMinPaneWidth:  cfg.Diff.MinPaneWidth,
		avatarColors:      map[string]string{},
		help:              newHelp(),
		keys:              keys,
	}
	if localDetail {
		if err := m.loadLocal(st, cache, currentPR); err != nil {
			return Model{}, err
		}
	}
	m.seedPRPages()
	m.prList.activePage = prPageKey(m.prList.view, m.prList.state, m.prList.filterQuery)
	page := m.prList.pages[m.prList.activePage]
	page.loading = true
	m.prList.pages[m.prList.activePage] = page
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

// isCurrentTargetPR reports whether the PR matches what's already loaded in the
// detail screen — either by branch (non-remote, current branch) or by explicit
// checkout number. Matching by branch alone is insufficient when multiple PRs
// share the same head, so we also require the PR number to match if one is known.
func (m Model) isCurrentTargetPR(pr gh.PR) bool {
	if !m.remote && m.cache.ExplicitCheckout && m.cache.PR != nil && m.cache.PR.Number == pr.Number {
		return true
	}
	if !isCurrentPR(pr, m.currentBranch) {
		return false
	}
	if m.cache.PR != nil && pr.Number > 0 && m.cache.PR.Number != pr.Number {
		return false
	}
	return true
}

func (m Model) currentPRNumber() int {
	if m.cache.PR != nil {
		return m.cache.PR.Number
	}
	return 0
}

// cachedDetail snapshots the cache as the previous detail so LoadPRDetail can
// refresh comments incrementally. LoadPRDetail only trusts the snapshot when
// its PR number matches the one being fetched.
func (m Model) cachedDetail() gh.PRDetail {
	detail := gh.PRDetail{Comments: m.cache.Comments}
	if m.cache.PR != nil {
		detail.PR = *m.cache.PR
	}
	return detail
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

func publishedReviewHead(pr *gh.PR) string {
	if pr == nil || pr.Number <= 0 {
		return ""
	}
	if pr.HeadRefOID != "" {
		return pr.HeadRefOID
	}
	return fmt.Sprintf("refs/live-pr/pulls/%d/head", pr.Number)
}

func localReviewRange(base string, pr *gh.PR, currentHead string, remote bool) (diffBase, headRev, reviewRange string) {
	diffBase = localReviewBase(base, pr)
	if head := publishedReviewHead(pr); head != "" {
		return remoteReviewBase(*pr), head, remoteReviewBase(*pr) + "..." + head
	}
	if remote {
		return diffBase, currentHead, diffBase + "..." + currentHead
	}
	return diffBase, "HEAD", diffBase
}

func (m *Model) applyLocal(st *store.Store, data localData) {
	m.detailView.resetCaches()
	if m.remote {
		// Coming from a remote PR is a target switch; a local reload of the
		// same branch keeps the rendered mermaid.
		m.detailView.pruneRichContent()
	}
	if data.incomplete {
		m.status = "local git data is incomplete"
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	cache := data.cache
	prURL := ""
	if cache.PR != nil {
		prURL = cache.PR.URL
	}
	m.screen = detailScreen
	m.remote = false
	m.detailView.summary = data.conclusion
	m.detailView.title = prbody.Title(m.detailView.summary, st.Branch)
	if cache.PR != nil && strings.TrimSpace(cache.PR.Title) != "" {
		m.detailView.title = cache.PR.Title
	}
	m.localAvailable, m.localTitle = cache.PR == nil, m.detailView.title
	m.localStats, m.localCommitCount, m.workingTreeDirty = data.stats, len(data.commits), data.dirty
	m.detailView.mergeReadiness, m.detailView.mergeReadinessErr = data.mergeReadiness, data.mergeReadinessErr
	m.detailView.base, m.detailView.diffBase, m.detailView.head, m.detailView.headRev, m.detailView.reviewRange = data.base, data.diffBase, st.Branch, data.headRev, data.reviewRange
	m.detailView.events, m.detailView.files, m.detailView.commits = data.events, data.files, data.commits
	m.timelinePath, m.cachePath, m.cache = st.Timeline(), st.GitHubCache(), cache
	m.loadReviewedMarks(m.currentPRNumber(), st.Branch)
	m.refreshReviewDraft()
	m.detailView.invalidateConversation()
	m.githubStatus = "Local only · checking for PR…"
	if cache.PR != nil {
		m.githubStatus = "GitHub: cached · refreshing…"
	}
	m.refreshing, m.publishing = true, false
	m.diffTerminal = embeddedterm.New(m.diffCommand, m.root, embeddedterm.Environment(m.detailView.reviewRange, data.diffBase, st.Branch, "HEAD", prURL, "", m.detailView.reviewedMarksPath))
	m.detailView.focusDiff, m.detailView.focusExplorer, m.detailView.fileCursor, m.detailView.active, m.detailView.reviewSHA = false, false, 0, conversationTab, ""
	m.layout()
}

// Run launches the TUI.
// Option configures the TUI before launch.
type Option func(*Model)

// WithDiff overrides the diff viewer by preset name or command.
func WithDiff(name string) Option {
	return func(m *Model) {
		if name == "" {
			return
		}
		cfg := config.Config{Diff: config.DiffConfig{
			Command:       m.diffCommand,
			CommitCommand: m.diffCommitCommand,
			Display:       m.diffDisplay,
		}}
		cfg.ApplyDiffPreset(name)
		m.diffCommand = cfg.Diff.Command
		m.diffCommitCommand = cfg.Diff.CommitCommand
		m.diffDisplay = cfg.Diff.Display
	}
}

func Run(version string, opts ...Option) error {
	m, err := New(version)
	if err != nil {
		return err
	}
	for _, opt := range opts {
		opt(&m)
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
	// loadSpinner and spinnerRunning are initialized in New(); the value
	// receiver here would discard any mutation anyway.
	cmds := []tea.Cmd{fetchPRList(m.client, m.prList.generation, m.prList.activePage, m.prViewSearch(m.prList.view, m.prList.state, m.prList.filterQuery), "", false), m.loadSpinner.Tick}
	if m.screen == prListScreen && m.localAvailable && m.autoOpenCurrent {
		cmds = append(cmds, fetchCurrentBranchPR(m.client, m.currentBranch))
	}
	if m.screen == detailScreen && !m.remote && m.cachePath != "" {
		cmds = append(cmds, fetchGitHub(m.client, m.detailView.head, m.currentPRNumber(), m.targetGeneration, m.cachedDetail()))
	}
	if m.screen == detailScreen {
		cmds = append(cmds, m.richContentCmd())
	}
	if m.diffTerminal != nil {
		cmds = append(cmds, m.diffTerminal.Init())
	}
	return tea.Batch(cmds...)
}

func (m Model) isLoading() bool {
	return m.refreshing || m.prList.refreshing || m.publishing || m.reviewSubmitting || m.prStatusRunning() || m.remoteCommentBusy || m.prActionRunning != noPRAction || len(m.prList.previewLoading) > 0 || len(m.detailView.diffPending) > 0
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

func (m *Model) close() {
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
}

func (m *Model) advanceAsyncGenerations(previous Model) {
	m.targetGeneration = previous.targetGeneration + 1
	m.prList.generation = previous.prList.generation + 1
	m.detailView.resetCaches()
}

func (d *detailModel) invalidateConversation() {
	d.conversationDirty = true
	// The render caches must drop too: conversationItems() consumes the dirty
	// flag, and callers like restoreConversationSelection do that before
	// buildConversation runs — the stale render would otherwise survive a
	// refresh whenever cursor, width, and item count all stayed the same.
	d.conversationRenderValid = false
	d.convItemCache = map[string][]string{}
}

func (m *Model) openRemote(pr gh.PR) tea.Cmd {
	m.targetGeneration++
	m.detailView.resetCaches()
	if !m.remote || m.cache.PR == nil || m.cache.PR.Number != pr.Number {
		// Reopening the same PR keeps its rendered mermaid; a different
		// target's bodies would never be looked up again.
		m.detailView.pruneRichContent()
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	m.screen, m.remote = detailScreen, true
	m.detailView.title = pr.Title
	m.detailView.base = git.ResolveBase(pr.BaseRefName)
	m.detailView.diffBase = remoteReviewBase(pr)
	m.detailView.head = pr.HeadRefName
	m.detailView.headRev = fmt.Sprintf("refs/live-pr/pulls/%d/head", pr.Number)
	m.detailView.reviewRange = m.detailView.diffBase + "..." + m.detailView.headRev
	m.detailView.events, m.detailView.commits, m.detailView.files = nil, nil, nil
	m.detailView.mergeReadiness, m.detailView.mergeReadinessErr = git.MergeReadiness{}, nil
	m.timelinePath, m.cachePath = "", ""
	m.cache = gh.NewCache(pr.HeadRefName)
	m.cache.PR = &pr
	m.loadReviewedMarks(pr.Number, pr.HeadRefName)
	m.refreshReviewDraft()
	if snapshot, ok := m.navigator.Snapshot(pr.Number); ok {
		m.cache.Comments = snapshot.Comments
		m.cache.Activities = snapshot.Activities
		m.cache.FetchedAt = snapshot.FetchedAt
	}
	m.detailView.invalidateConversation()
	m.detailView.reviewSHA, m.detailView.active, m.detailView.focusDiff, m.detailView.focusExplorer, m.detailView.fileCursor = "", conversationTab, false, false, 0
	m.diffTerminal = nil
	m.refreshing, m.publishing = true, false
	m.status = "loading PR refs…"
	m.githubStatus = "GitHub: cached · refreshing selected PR…"
	m.layout()
	return tea.Batch(fetchRemotePR(m.client, pr, m.targetGeneration, m.cachedDetail()), m.sync(), m.startSpinner())
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
	// PreviewUp/PreviewDown never reach here: every caller scrolls the
	// preview viewport on those keys before delegating.
	return false, nil
}

func (m *Model) navigationLength() int {
	if m.screen == prListScreen {
		return len(m.prList.open)
	}
	if m.detailView.focusExplorer {
		return len(m.detailView.files)
	}
	return m.activeLen()
}

func scrollQuarter(v *viewport.Model, down bool) {
	lines := max(1, v.Height/4)
	if down {
		v.ScrollDown(lines)
	} else {
		v.ScrollUp(lines)
	}
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
		m.prList.cursor = index
		cmd := m.sync()
		if index == length-1 {
			return tea.Batch(cmd, m.requestPRPage(false))
		}
		return cmd
	} else if m.detailView.focusExplorer {
		m.detailView.fileCursor = index
	} else {
		m.detailView.cursors[m.detailView.active] = index
	}
	return m.sync()
}

func (m Model) navigationCursor() int {
	if m.screen == prListScreen {
		return m.prList.cursor
	}
	if m.detailView.focusExplorer {
		return m.detailView.fileCursor
	}
	return m.detailView.cursors[m.detailView.active]
}

// isDefaultBranch reports whether ref names the repository's default branch.
// Local revisions carry the origin/ prefix that GitHub's ref names lack.
func (m Model) isDefaultBranch(ref string) bool {
	if m.defaultBranch == "" || ref == "" {
		return false
	}
	return strings.EqualFold(strings.TrimPrefix(ref, "origin/"), m.defaultBranch)
}

// listViewForReturn picks the tab to land on when leaving the detail screen.
// Entering from a tab returns there; a detail opened at startup has no origin,
// so the first tab that actually contains the PR wins, falling back to the
// first tab.
func (m Model) listViewForReturn(number int) prView {
	if m.detailOriginSet && int(m.detailOrigin) < len(m.views) {
		return m.detailOrigin
	}
	pr := m.cache.PR
	if pr == nil {
		return 0
	}
	for view := prView(0); int(view) < len(m.views); view++ {
		if matchesListState(*pr, m.standardPRListState(view)) && m.matchesView(*pr, view) {
			return view
		}
	}
	return 0
}

func (m Model) selectedBrowseURL() string {
	if m.screen == prListScreen {
		if pr := m.prList.selectedPR(); pr != nil && pr.URL != "" {
			return pr.URL
		}
		return ""
	}
	// The commits and conflicts tabs have nothing per-row to link to, so
	// they fall back to the pull request itself — as does a conversation
	// row without its own URL, or a check without a log page.
	prURL := ""
	if m.cache.PR != nil {
		prURL = m.cache.PR.URL
	}
	if m.detailView.active == checksTab {
		if i := m.detailView.cursors[checksTab]; m.cache.PR != nil && i >= 0 && i < len(m.cache.PR.Checks) {
			if url := m.cache.PR.Checks[i].URL(); url != "" {
				return url
			}
		}
		return prURL
	}
	if m.detailView.active != conversationTab {
		return prURL
	}
	item := m.selectedConversationItem()
	if item == nil {
		return prURL
	}
	if item.pr != nil && item.pr.URL != "" {
		return item.pr.URL
	}
	if item.comment != nil && item.comment.HTMLURL != "" {
		return item.comment.HTMLURL
	}
	return prURL
}

func (d detailModel) selectedCommitSHA() string {
	if i := d.cursors[commitsTab]; d.active == commitsTab && i >= 0 && i < len(d.commits) {
		return d.commits[i].SHA
	}
	return ""
}

// sync rebuilds both panes for the current tab and selection.
func (m *Model) sync() tea.Cmd {
	if done := debugtime.Start("tui sync"); done != nil {
		defer done()
	}
	if !m.ready {
		return nil
	}
	start := m.startSpinner()
	if m.screen == prListScreen {
		return m.syncPRListScreen(start)
	}
	return m.syncDetailScreen(start)
}

// applyKeyStates flips every listed binding at once, so each screen declares
// its keys as a predicate table instead of forty ordered SetEnabled calls.
func applyKeyStates(states map[*key.Binding]bool) {
	for binding, enabled := range states {
		binding.SetEnabled(enabled)
	}
}

func (m *Model) syncPRListScreen(start tea.Cmd) tea.Cmd {
	pr := m.prList.selectedPR()
	_, stacked := m.prList.stackForPR(m.prList.selectedPRNumber())
	idle := m.prActionRunning == noPRAction
	applyKeyStates(map[*key.Binding]bool{
		&m.keys.Select:      true,
		&m.keys.PreviewUp:   true,
		&m.keys.PreviewDown: true,
		&m.keys.PrevView:    true,
		&m.keys.NextView:    true,
		&m.keys.Filter:      true,
		&m.keys.ToggleStack: stacked,
		&m.keys.Focus:       false,
		&m.keys.FocusRight:  false,
		&m.keys.Commits:     false,
		&m.keys.Conflicts:   false,
		&m.keys.Checks:      false,
		&m.keys.Back:        false,
		&m.keys.PRList:      false,
		&m.keys.Browse:      m.selectedBrowseURL() != "",
		&m.keys.CopyURL:     m.selectedBrowseURL() != "",
		&m.keys.Publish:     false,
		&m.keys.Merge:       m.prList.state == openPRListState && pr != nil && pr.Number > 0 && pr.HeadRefOID != "" && idle,
		&m.keys.Checkout:    pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) && idle,
		&m.keys.Close:       m.prList.state == openPRListState && pr != nil && pr.Number > 0 && idle,
		&m.keys.Status:      pr != nil && pr.Number > 0,
		&m.keys.ManageViews: true,
	})
	content, selectedLine := m.buildPRListRows()
	m.list.SetContent(content)
	m.detail.SetContent(m.buildPRPreview())
	// The preview shares the detail viewport, so the detail screen's
	// shown-content marker no longer describes what is displayed.
	m.detailView.shownKey = ""
	// Background arrivals (previews, avatars) re-sync constantly; only a
	// selection change may reset the preview scroll position.
	if selected := m.prList.selectedPRNumber(); selected != m.prList.previewedPR {
		m.prList.previewedPR = selected
		m.detail.GotoTop()
	}
	keepLineVisible(&m.list, selectedLine)
	preview := m.ensureSelectedPRPreview()
	return tea.Batch(start, preview, m.startSpinner())
}

func (m *Model) syncDetailScreen(start tea.Cmd) tea.Cmd {
	applyKeyStates(map[*key.Binding]bool{
		&m.keys.Select:      m.detailView.active == commitsTab,
		&m.keys.PreviewUp:   true,
		&m.keys.PreviewDown: true,
		&m.keys.PrevView:    false,
		&m.keys.NextView:    false,
		&m.keys.Filter:      false,
		&m.keys.ToggleStack: false,
		&m.keys.Focus:       true,
		&m.keys.FocusRight:  true,
		&m.keys.Commits:     m.fileExplorerMode() || m.detailView.active != commitsTab,
		&m.keys.Conflicts:   m.detailView.active != conflictsTab,
		&m.keys.Checks:      m.cache.PR != nil && m.detailView.active != checksTab,
		&m.keys.Back:        m.detailView.active != conversationTab,
		&m.keys.PRList:      true,
		&m.keys.Browse:      m.selectedBrowseURL() != "",
		&m.keys.CopyURL:     m.selectedBrowseURL() != "",
		&m.keys.Publish:     !m.remote,
		&m.keys.Merge:       m.canMergeCurrentPR() && m.prActionRunning == noPRAction,
		&m.keys.Checkout:    m.cache.PR != nil && m.cache.PR.Number > 0 && !m.isCurrentTargetPR(*m.cache.PR) && m.prActionRunning == noPRAction,
		&m.keys.Close:       false,
		&m.keys.Status:      m.cache.PR != nil && m.cache.PR.Number > 0,
		&m.keys.ManageViews: false,
	})
	content, selectedLine := m.buildList()
	m.list.SetContent(content)
	// Chase the selection only when it actually moved. Syncing runs on every
	// background arrival and on every reload, and scrolling back to the
	// selection there would drag a reader who had scrolled elsewhere.
	if anchor := (listAnchor{tab: m.detailView.active, line: selectedLine, pinned: true}); anchor != m.detailView.listAnchor {
		m.detailView.listAnchor = anchor
		keepLineVisible(&m.list, selectedLine)
	}
	if m.fileExplorerMode() {
		explorer, selectedFileLine := m.buildFileExplorer()
		m.explorer.SetContent(explorer)
		keepLineVisible(&m.explorer, selectedFileLine)
	}

	detail, rawCmd := m.loadDetail()
	detailCmd := m.syncDetail(detail)
	return tea.Batch(start, rawCmd, detailCmd, m.startSpinner())
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

func first(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
