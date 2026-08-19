// Package tui renders Conversation beside a branch- or commit-scoped review.
package tui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	bspinner "github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/clipboard"
	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/debugtime"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prbody"
	"github.com/shonenm/live-pr/internal/prtemplate"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/richcontent"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
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
// three hand-maintained lists (FocusLeft already had).
func (k keyMap) helpGroups() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PreviewUp, k.PreviewDown, k.Top, k.Bottom, k.PrevView, k.NextView, k.Filter, k.ToggleStack, k.Focus, k.FocusRight, k.FocusLeft, k.Commits, k.Conflicts, k.Checks, k.Select, k.Back, k.PRList},
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
	// stateOnly marks a refresh-triggered lookup: it updates what the list
	// knows about the branch's PR without moving the user to another tab or
	// disturbing the selection.
	stateOnly bool
}

type prPreviewLoaded struct {
	generation uint64
	number     int
	pr         gh.PR
	err        error
}

type remoteLoaded struct {
	generation        uint64
	pr                gh.PR
	headRef           string
	base              string
	diffBase          string
	commits           []git.Commit
	files             []git.ChangedFile
	comments          []gh.Comment
	activities        []gh.Activity
	reviews           []gh.Review
	reviewComments    []gh.ReviewThreadComment
	readiness         git.MergeReadiness
	refErr            error
	previewErr        error
	commentsErr       error
	activitiesErr     error
	reviewsErr        error
	reviewCommentsErr error
	readinessErr      error
}

type ciPollTick struct {
	generation uint64
	number     int
}

type ciPolled struct {
	generation uint64
	pr         gh.PR
	err        error
}

type githubRefreshed struct {
	generation        uint64
	pr                gh.PR
	comments          []gh.Comment
	activities        []gh.Activity
	reviews           []gh.Review
	reviewComments    []gh.ReviewThreadComment
	err               error
	commentsErr       error
	activitiesErr     error
	reviewsErr        error
	reviewCommentsErr error
}

type publishDone struct {
	generation uint64
	result     publish.Result
	err        error
}

type browserDone struct {
	err    error
	copied bool
}

type navigatorCacheSaved struct {
	err error
}

type cacheSaved struct {
	err error
}

// saveCacheCmd persists the branch GitHub cache off the Update goroutine. The
// PR is copied here because handlers mutate it in place (CI polls); slices are
// only ever replaced wholesale, so sharing them is safe.
func saveCacheCmd(path string, cache gh.Cache) tea.Cmd {
	if cache.PR != nil {
		pr := *cache.PR
		cache.PR = &pr
	}
	return func() tea.Msg {
		return cacheSaved{err: gh.SaveCache(path, cache)}
	}
}

type baseResolved struct {
	generation                           uint64
	prURL                                string
	base, diffBase, headRev, reviewRange string
	events                               []event.Event
	eventsOK                             bool
	commits                              []git.Commit
	files                                []git.ChangedFile
	readiness                            git.MergeReadiness
	readinessErr                         error
	readinessOK                          bool
}

// saveNavigatorCacheCmd persists the navigator cache off the Update goroutine.
// The clone happens here, on the Update goroutine, so the write never races
// with later handler mutations; only failures produce a message.
func saveNavigatorCacheCmd(path string, navigator gh.NavigatorCache) tea.Cmd {
	snapshot := navigator.Clone()
	return func() tea.Msg {
		if err := gh.SaveNavigatorCache(path, snapshot); err != nil {
			return navigatorCacheSaved{err: err}
		}
		return nil
	}
}

type checkoutReloaded struct {
	number int
	next   *Model
	err    error
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

type reviewSubmitted struct {
	event gh.ReviewEvent
	err   error
}

type prStatusDone struct {
	pr     gh.PR
	target string
	err    error
}

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

type richBodiesLoaded struct {
	generation uint64
	key        [sha256.Size]byte
	bodies     map[string]string
}

type avatarColorsLoaded struct {
	generation uint64
	key        [sha256.Size]byte
	colors     map[string]string
}

type listAvatarColorsLoaded struct {
	generation uint64
	colors     map[string]string
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
	conversationRender        string
	conversationRenderLine    int
	conversationRenderKey     convRenderKey
	conversationRenderValid   bool
	richBodies                map[string]string
	avatarColors              map[string]string
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
	listAnchor                listAnchor
	detailOrigin              prView
	detailOriginSet           bool
	views                     []config.View
	viewManager               bool
	viewDraft                 []config.View
	viewCursor                int
	viewEditField             viewEditField
	viewEditIndex             int
	viewNameInput             textinput.Model
	viewQueryInput            textinput.Model
	viewManagerError          string
	prListState               prListState
	prPages                   map[string]prPageState
	activePRPage              string
	viewCounts                []int
	viewCountKnown            []bool
	viewCountsValid           bool
	filterQuery               string
	filterBeforeEdit          string
	filterSelectionBeforeEdit int
	filterEditing             bool
	prStacks                  []prStack
	prRowCache                map[prRowCacheKey][]string
	collapsedStacks           map[string]bool
	prCursor                  int
	convItemCache             map[string][]string
	previewedPR               int
	lastRichContentKey        [sha256.Size]byte
	localAvailable            bool
	localTitle                string
	localStats                git.ChangeStats
	localCommitCount          int
	workingTreeDirty          bool
	mergeReadiness            git.MergeReadiness
	mergeReadinessErr         error
	autoOpenCurrent           bool
	refreshing                bool
	ciPollFailures            int
	listRefreshing            bool
	publishing                bool
	localEditMode             localEditMode
	localEditor               textarea.Model
	localEditTarget           string
	localEditError            string
	remoteCommentID           int64
	remoteCommentBusy         bool
	localDeleteTarget         string
	localDeleteTitle          string
	remoteDeleteID            int64
	remoteDeleteTitle         string
	reviewDraft               gh.ReviewDraft
	reviewDraftPath           string
	reviewSubmitEvent         gh.ReviewEvent
	reviewSubmitCursor        int
	reviewSubmitTyping        bool
	reviewSubmitting          bool
	statusPR                  gh.PR
	statusCursor              int
	statusRunning             bool
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
	diffSplitRatio    int
	diffMinPaneWidth  int
	diffTerminal      *embeddedterm.Terminal
	focusDiff         bool
	focusExplorer     bool
	reviewWide        bool
	fileCursor        int
	detailKey         string
	detailShownKey    string
	rawDetailCache    map[string]string
	rawPending        map[string]bool
	diffCache         map[string]string
	diffPending       map[string]bool
	checkedFiles      map[string]string
	reviewedMarksPath string

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
		screen:            prListScreen,
		version:           first(version),
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
		views:             views,
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
		diffSplitRatio:    cfg.Diff.SplitRatio,
		diffMinPaneWidth:  cfg.Diff.MinPaneWidth,
		rawDetailCache:    map[string]string{},
		diffCache:         map[string]string{},
		richBodies:        map[string]string{},
		avatarColors:      map[string]string{},
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

// localData is everything loadLocalData gathers off the Update goroutine; the
// git subprocesses involved froze the UI when run inside a message handler.
type localData struct {
	cache             gh.Cache
	base              string
	diffBase          string
	headRev           string
	reviewRange       string
	events            []event.Event
	commits           []git.Commit
	files             []git.ChangedFile
	stats             git.ChangeStats
	dirty             bool
	incomplete        bool
	conclusion        string
	mergeReadiness    git.MergeReadiness
	mergeReadinessErr error
}

type localLoaded struct {
	generation uint64
	st         *store.Store
	data       localData
	err        error
}

// startLocalLoad gathers local detail in a Cmd and applies it on localLoaded.
func (m *Model) startLocalLoad(st *store.Store, cache gh.Cache, hintedPR *gh.PR) tea.Cmd {
	m.targetGeneration++
	generation := m.targetGeneration
	m.refreshing = true
	var hint *gh.PR
	if hintedPR != nil {
		pr := *hintedPR
		hint = &pr
	}
	return func() tea.Msg {
		data, err := loadLocalData(st, cache, hint)
		return localLoaded{generation: generation, st: st, data: data, err: err}
	}
}

// loadLocal is the synchronous variant for startup, before the UI runs.
func (m *Model) loadLocal(st *store.Store, cache gh.Cache, hintedPR *gh.PR) error {
	m.targetGeneration++
	data, err := loadLocalData(st, cache, hintedPR)
	if err != nil {
		return err
	}
	m.applyLocal(st, data)
	return nil
}

func loadLocalData(st *store.Store, cache gh.Cache, hintedPR *gh.PR) (localData, error) {
	if done := debugtime.Start("tui local hydration"); done != nil {
		defer done()
	}
	if err := st.Ensure(); err != nil {
		return localData{}, err
	}
	if err := prtemplate.Seed(st); err != nil {
		return localData{}, err
	}
	if cache.PR == nil && hintedPR != nil && hintedPR.Number > 0 {
		pr := *hintedPR
		cache.PR = &pr
	}
	base := git.ResolveBase(cache.Base(git.DefaultBase()))
	diffBase, headRev, reviewRange := localReviewRange(base, cache.PR, "HEAD", false)
	_, _ = timeline.SyncCommits(st.Timeline(), diffBase)
	events, err := event.Load(st.Timeline())
	if err != nil {
		return localData{}, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	var commits []git.Commit
	var files []git.ChangedFile
	var stats git.ChangeStats
	var commitErr, fileErr error
	if headRev == "HEAD" {
		commits, commitErr = git.Commits(diffBase)
		files, fileErr = git.ChangedFilesRange(diffBase, "")
		stats, _ = git.DiffStats(diffBase, "")
	} else {
		commits, commitErr = git.CommitsRange(diffBase, headRev)
		files, fileErr = git.ChangedFilesRange(diffBase, headRev)
		stats, _ = git.DiffStats(diffBase, headRev)
	}
	dirty, dirtyErr := git.HasUncommittedChanges()
	conclusion, _ := os.ReadFile(st.Conclusion())
	mergeReadiness, mergeReadinessErr := git.CheckMergeReadiness(base, "HEAD")
	return localData{
		cache:             cache,
		base:              base,
		diffBase:          diffBase,
		headRev:           headRev,
		reviewRange:       reviewRange,
		events:            events,
		commits:           commits,
		files:             files,
		stats:             stats,
		dirty:             dirty,
		incomplete:        commitErr != nil || fileErr != nil || dirtyErr != nil,
		conclusion:        string(conclusion),
		mergeReadiness:    mergeReadiness,
		mergeReadinessErr: mergeReadinessErr,
	}, nil
}

func (m *Model) applyLocal(st *store.Store, data localData) {
	m.resetDetailCaches()
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
	m.summary = data.conclusion
	m.title = prbody.Title(m.summary, st.Branch)
	if cache.PR != nil && strings.TrimSpace(cache.PR.Title) != "" {
		m.title = cache.PR.Title
	}
	m.localAvailable, m.localTitle = cache.PR == nil, m.title
	m.localStats, m.localCommitCount, m.workingTreeDirty = data.stats, len(data.commits), data.dirty
	m.mergeReadiness, m.mergeReadinessErr = data.mergeReadiness, data.mergeReadinessErr
	m.base, m.diffBase, m.head, m.headRev, m.reviewRange = data.base, data.diffBase, st.Branch, data.headRev, data.reviewRange
	m.events, m.files, m.commits = data.events, data.files, data.commits
	m.timelinePath, m.cachePath, m.cache = st.Timeline(), st.GitHubCache(), cache
	m.loadReviewedMarks(m.currentPRNumber(), st.Branch)
	m.invalidateConversation()
	m.githubStatus = "Local only · checking for PR…"
	if cache.PR != nil {
		m.githubStatus = "GitHub: cached · refreshing…"
	}
	m.refreshing, m.publishing = true, false
	m.diffTerminal = embeddedterm.New(m.diffCommand, m.root, embeddedterm.Environment(m.reviewRange, data.diffBase, st.Branch, "HEAD", prURL, "", m.reviewedMarksPath))
	m.focusDiff, m.focusExplorer, m.fileCursor, m.active, m.reviewSHA = false, false, 0, conversationTab, ""
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
	cmds := []tea.Cmd{fetchPRList(m.prListGeneration, m.activePRPage, m.prViewSearch(m.prView, m.prListState, m.filterQuery), "", false), m.loadSpinner.Tick}
	if m.screen == prListScreen && m.localAvailable && m.autoOpenCurrent {
		cmds = append(cmds, fetchCurrentBranchPR(m.currentBranch))
	}
	if m.screen == detailScreen && !m.remote && m.cachePath != "" {
		cmds = append(cmds, fetchGitHub(m.head, m.currentPRNumber(), m.targetGeneration))
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
	return m.refreshing || m.listRefreshing || m.publishing || m.reviewSubmitting || m.statusRunning || m.remoteCommentBusy || m.prActionRunning != noPRAction || len(m.prPreviewLoading) > 0 || len(m.diffPending) > 0
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
	// The render caches must drop too: conversationItems() consumes the dirty
	// flag, and callers like restoreConversationSelection do that before
	// buildConversation runs — the stale render would otherwise survive a
	// refresh whenever cursor, width, and item count all stayed the same.
	m.conversationRenderValid = false
	m.convItemCache = map[string][]string{}
}

func fetchPRList(generation uint64, key, query, cursor string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		page, err := gh.New().SearchPRs(query, cursor)
		return prListRefreshed{generation: generation, key: key, appendPage: appendPage, page: page, err: err}
	}
}

// fetchCurrentBranchPRState re-checks the branch's PR purely to refresh what
// the list believes about it.
func fetchCurrentBranchPRState(head string) tea.Cmd {
	return func() tea.Msg {
		msg, _ := fetchCurrentBranchPR(head)().(currentBranchPRLoaded)
		msg.stateOnly = true
		return msg
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
		return githubRefreshed{generation: generation, pr: detail.PR, comments: detail.Comments, activities: detail.Activities, reviews: detail.Reviews, reviewComments: detail.ReviewComments, err: detail.PreviewErr, commentsErr: detail.CommentsErr, activitiesErr: detail.ActivitiesErr, reviewsErr: detail.ReviewsErr, reviewCommentsErr: detail.ReviewCommentsErr}
	}
}

func scheduleCIPoll(generation uint64, number, failures int) tea.Cmd {
	delay := 15 * time.Second
	if failures > 0 {
		delay *= time.Duration(1 << min(failures, 3))
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return ciPollTick{generation: generation, number: number}
	})
}

func (m Model) nextCIPoll() tea.Cmd {
	if m.screen == detailScreen && m.cache.PR != nil && pollableCI(*m.cache.PR) {
		return scheduleCIPoll(m.targetGeneration, m.cache.PR.Number, m.ciPollFailures)
	}
	return nil
}

// pollableCI reports whether a PR's checks are still worth polling. Only an
// open PR can change them; a sparse row with no state yet is given the
// benefit of the doubt.
func pollableCI(pr gh.PR) bool {
	if pr.State != "" && !strings.EqualFold(pr.State, "OPEN") {
		return false
	}
	return prCIHealth(pr) == "pending"
}

func pollCI(generation uint64, number int) tea.Cmd {
	return func() tea.Msg {
		pr, err := gh.New().FindChecks(number)
		return ciPolled{generation: generation, pr: pr, err: err}
	}
}

func richContentKey(width int, pr *gh.PR, comments []gh.Comment, activities []gh.Activity) [sha256.Size]byte {
	var input strings.Builder
	// Width participates in the key so a resize invalidates rendered mermaid.
	fmt.Fprintf(&input, "%d\x00", width)
	if pr != nil {
		fmt.Fprintf(&input, "%s\x00%s\x00%s\x00", pr.Author.Login, pr.Author.AvatarURL, pr.Body)
	}
	for _, comment := range comments {
		fmt.Fprintf(&input, "%s\x00%s\x00%s\x00", comment.User.Login, comment.User.AvatarURL, comment.Body)
	}
	for _, activity := range activities {
		fmt.Fprintf(&input, "%s\x00%s\x00", activity.Actor.Login, activity.Actor.AvatarURL)
	}
	return sha256.Sum256([]byte(input.String()))
}

func loadAvatarColors(avatars map[string]string) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	colors := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for login, avatarURL := range avatars {
		if login == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if avatarURL == "" {
				avatarURL = "https://avatars.githubusercontent.com/" + login
			}
			if color, err := richcontent.AvatarColorContext(ctx, avatarURL); err == nil {
				mu.Lock()
				colors[login] = color
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return colors
}

func loadListAvatarColors(generation uint64, prs []gh.PR) tea.Cmd {
	avatars := map[string]string{}
	for _, pr := range prs {
		avatars[pr.Author.Login] = pr.Author.AvatarURL
		for _, comment := range pr.Conversation {
			avatars[comment.Author.Login] = comment.Author.AvatarURL
		}
		for _, user := range pr.Assignees {
			avatars[user.Login] = user.AvatarURL
		}
		for _, user := range pr.ReviewRequests {
			avatars[user.Login] = user.AvatarURL
		}
	}
	return func() tea.Msg {
		return listAvatarColorsLoaded{generation: generation, colors: loadAvatarColors(avatars)}
	}
}

// richContentCmd dispatches mermaid rendering and avatar resolution only when
// the content key (bodies + width) changed since the last dispatch: refreshes
// with unchanged conversations re-rendered every diagram and re-downloaded
// every avatar otherwise.
func (m *Model) richContentCmd() tea.Cmd {
	width := m.list.Width - 7
	if width <= 0 {
		// Init can run before the first WindowSizeMsg; rendering mermaid at a
		// negative width wastes the work and caches garbage.
		return nil
	}
	key := richContentKey(width, m.cache.PR, m.cache.Comments, m.cache.Activities)
	if key == m.lastRichContentKey {
		return nil
	}
	m.lastRichContentKey = key
	resolved := make(map[string]bool, len(m.avatarColors))
	for login := range m.avatarColors {
		resolved[login] = true
	}
	return loadRichContent(m.targetGeneration, width, m.cache.PR, m.cache.Comments, m.cache.Activities, resolved)
}

func loadRichContent(generation uint64, width int, pr *gh.PR, comments []gh.Comment, activities []gh.Activity, resolved map[string]bool) tea.Cmd {
	key := richContentKey(width, pr, comments, activities)
	bodies := make([]string, 0, len(comments)+1)
	avatars := map[string]string{}
	if pr != nil {
		bodies = append(bodies, pr.Body)
		avatars[pr.Author.Login] = pr.Author.AvatarURL
	}
	for _, comment := range comments {
		bodies = append(bodies, comment.Body)
		avatars[comment.User.Login] = comment.User.AvatarURL
	}
	for _, activity := range activities {
		avatars[activity.Actor.Login] = activity.Actor.AvatarURL
	}
	for login := range resolved {
		delete(avatars, login)
	}
	return tea.Batch(
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			results := map[string]string{}
			for _, body := range bodies {
				rendered := map[string]string{}
				for _, source := range richcontent.MermaidSources(body) {
					if len(rendered) >= 32 {
						break
					}
					if diagram, err := richcontent.RenderMermaidContext(ctx, source, width); err == nil {
						rendered[source] = diagram
					}
				}
				results[body] = richcontent.ReplaceMermaid(body, rendered)
			}
			return richBodiesLoaded{generation: generation, key: key, bodies: results}
		},
		func() tea.Msg {
			return avatarColorsLoaded{generation: generation, key: key, colors: loadAvatarColors(avatars)}
		},
	)
}

func fetchRemotePR(pr gh.PR, generation uint64) tea.Cmd {
	return func() tea.Msg {
		client := gh.New()
		var headRef string
		var comments []gh.Comment
		var activities []gh.Activity
		var reviews []gh.Review
		var reviewComments []gh.ReviewThreadComment
		var refErr, previewErr, commentsErr, activitiesErr, reviewsErr, reviewCommentsErr, readinessErr error
		var readiness git.MergeReadiness
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
			reviews, reviewComments = detail.Reviews, detail.ReviewComments
			previewErr, commentsErr, activitiesErr = detail.PreviewErr, detail.CommentsErr, detail.ActivitiesErr
			reviewsErr, reviewCommentsErr = detail.ReviewsErr, detail.ReviewCommentsErr
			if previewErr == nil {
				pr = detail.PR
			}
		}()
		wg.Wait()
		// The range scans run here so handleRemoteLoaded stays subprocess-free
		// on the Update goroutine.
		var resolvedBase, diffBase string
		var commits []git.Commit
		var files []git.ChangedFile
		if refErr == nil {
			resolvedBase = git.ResolveBase(pr.BaseRefName)
			diffBase = remoteReviewBase(pr)
			readiness, readinessErr = git.CheckMergeReadiness(resolvedBase, headRef)
			commits, _ = git.CommitsRange(diffBase, headRef)
			files, _ = git.ChangedFilesRange(diffBase, headRef)
		}
		return remoteLoaded{generation: generation, pr: pr, headRef: headRef, base: resolvedBase, diffBase: diffBase, commits: commits, files: files, comments: comments, activities: activities, reviews: reviews, reviewComments: reviewComments, readiness: readiness, refErr: refErr, previewErr: previewErr, commentsErr: commentsErr, activitiesErr: activitiesErr, reviewsErr: reviewsErr, reviewCommentsErr: reviewCommentsErr, readinessErr: readinessErr}
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
	m.mergeReadiness, m.mergeReadinessErr = git.MergeReadiness{}, nil
	m.timelinePath, m.cachePath = "", ""
	m.cache = gh.NewCache(pr.HeadRefName)
	m.cache.PR = &pr
	m.loadReviewedMarks(pr.Number, pr.HeadRefName)
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
	if m.w <= 0 || m.h <= 0 {
		return
	}
	if m.screen == prListScreen {
		bodyH := max(3, m.h-m.headerHeight()-footerLines-paneChromeH)
		ratio := m.diffSplitRatio
		if ratio <= 0 {
			ratio = prListPaneRatio
		}
		listPaneW := max(4, m.w*ratio/100)
		minPaneW := max(14, m.diffMinPaneWidth)
		if minPaneW <= 0 {
			minPaneW = 24
		}
		if listPaneW < minPaneW {
			listPaneW = minPaneW
		}
		if m.w-listPaneW < minPaneW {
			listPaneW = max(minPaneW, m.w-minPaneW)
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
	ratio := m.diffSplitRatio
	if ratio <= 0 {
		ratio = listRatio
	}
	minPaneW := max(14, m.diffMinPaneWidth)
	if minPaneW <= 0 {
		minPaneW = 24
	}
	if minPaneW > m.w/2 {
		minPaneW = max(8, m.w/2)
	}
	var leftPaneW, rightPaneW int
	conversationWide := m.reviewWide && !m.focusDiff && !m.focusExplorer
	if conversationWide {
		leftPaneW, rightPaneW = m.w, 0
	} else if m.reviewWide {
		leftPaneW, rightPaneW = 0, m.w
	} else {
		leftPaneW = max(minPaneW, m.w*ratio/100)
		rightPaneW = m.w - leftPaneW
		if rightPaneW < minPaneW {
			rightPaneW = minPaneW
			leftPaneW = max(minPaneW, m.w-minPaneW)
		}
	}
	listW, rightW := 0, max(4, rightPaneW-paneChromeW)
	if leftPaneW > 0 {
		listW = max(4, leftPaneW-paneChromeW)
	}
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
		m.list = viewport.New(listW, max(1, bodyH-1))
		m.explorer = viewport.New(explorerW, bodyH)
		m.detail = viewport.New(detailW, bodyH)
		m.ready = true
	} else {
		m.list.Width = listW
		m.list.Height = bodyH
		if m.active == conversationTab {
			m.list.Height = max(1, bodyH-1)
		}
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
	// PreviewUp/PreviewDown never reach here: every caller scrolls the
	// preview viewport on those keys before delegating.
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

// isDefaultBranch reports whether ref names the repository's default branch.
// Local revisions carry the origin/ prefix that GitHub's ref names lack.
func (m Model) isDefaultBranch(ref string) bool {
	if m.defaultBranch == "" || ref == "" {
		return false
	}
	return strings.EqualFold(strings.TrimPrefix(ref, "origin/"), m.defaultBranch)
}

// baseBranchStyle marks a merge target that is the repository's default
// branch, so a PR stacked on another branch stands out at a glance.
func (m Model) baseBranchStyle(ref string) lipgloss.Style {
	if m.isDefaultBranch(ref) {
		return stAccent.Bold(true)
	}
	return stBold
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
		if pr := m.selectedPR(); pr != nil && pr.URL != "" {
			return pr.URL
		}
		return ""
	}
	// The commits, conflicts, and checks tabs have nothing per-row to link
	// to, so they fall back to the pull request itself — as does a
	// conversation row without its own URL.
	prURL := ""
	if m.cache.PR != nil {
		prURL = m.cache.PR.URL
	}
	if m.active != conversationTab {
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

func (m Model) selectedCommitSHA() string {
	if i := m.cursors[commitsTab]; m.active == commitsTab && i >= 0 && i < len(m.commits) {
		return m.commits[i].SHA
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
	pr := m.selectedPR()
	_, stacked := m.stackForPR(m.selectedPRNumber())
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
		&m.keys.Merge:       m.prListState == openPRListState && pr != nil && pr.Number > 0 && pr.HeadRefOID != "" && idle,
		&m.keys.Checkout:    pr != nil && pr.Number > 0 && !m.isCurrentTargetPR(*pr) && idle,
		&m.keys.Close:       m.prListState == openPRListState && pr != nil && pr.Number > 0 && idle,
		&m.keys.Status:      pr != nil && pr.Number > 0,
		&m.keys.ManageViews: true,
	})
	content, selectedLine := m.buildPRListRows()
	m.list.SetContent(content)
	m.detail.SetContent(m.buildPRPreview())
	// The preview shares the detail viewport, so the detail screen's
	// shown-content marker no longer describes what is displayed.
	m.detailShownKey = ""
	// Background arrivals (previews, avatars) re-sync constantly; only a
	// selection change may reset the preview scroll position.
	if selected := m.selectedPRNumber(); selected != m.previewedPR {
		m.previewedPR = selected
		m.detail.GotoTop()
	}
	keepLineVisible(&m.list, selectedLine)
	preview := m.ensureSelectedPRPreview()
	return tea.Batch(start, preview, m.startSpinner())
}

func (m *Model) syncDetailScreen(start tea.Cmd) tea.Cmd {
	applyKeyStates(map[*key.Binding]bool{
		&m.keys.Select:      m.active == commitsTab,
		&m.keys.PreviewUp:   true,
		&m.keys.PreviewDown: true,
		&m.keys.PrevView:    false,
		&m.keys.NextView:    false,
		&m.keys.Filter:      false,
		&m.keys.ToggleStack: false,
		&m.keys.Focus:       true,
		&m.keys.FocusRight:  true,
		&m.keys.Commits:     m.fileExplorerMode() || m.active != commitsTab,
		&m.keys.Conflicts:   m.active != conflictsTab,
		&m.keys.Checks:      m.cache.PR != nil && m.active != checksTab,
		&m.keys.Back:        m.active != conversationTab,
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
	if anchor := (listAnchor{tab: m.active, line: selectedLine, pinned: true}); anchor != m.listAnchor {
		m.listAnchor = anchor
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

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	var view string
	if m.screen == prListScreen {
		listTitle := fmt.Sprintf("%s · %d", m.viewName(m.prView), len(m.filteredPRs))
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
		switch m.active {
		case commitsTab:
			leftTitle = fmt.Sprintf("Commits · %d", len(m.commits))
		case conflictsTab:
			leftTitle = fmt.Sprintf("Conflicts · %d", len(m.mergeReadiness.ConflictFiles))
		case checksTab:
			count := 0
			if m.cache.PR != nil {
				count = len(m.cache.PR.Checks)
			}
			leftTitle = fmt.Sprintf("Checks · %d", count)
		}
		if m.reviewWide && m.focusDiff {
			body := m.renderReviewPane()
			view = lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
		} else if m.reviewWide && !m.focusDiff {
			leftContent := m.list.View()
			if m.active == conversationTab {
				leftContent = lipgloss.JoinVertical(lipgloss.Left, leftContent, m.conversationCounts())
			}
			height := max(3, m.h-m.headerHeight()-footerLines)
			left := renderPane(leftTitle, leftContent, m.w, height, true)
			view = lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), left, m.renderFooter())
		} else {
			leftContent := m.list.View()
			if m.active == conversationTab {
				leftContent = lipgloss.JoinVertical(lipgloss.Left, leftContent, m.conversationCounts())
			}
			left := renderPane(leftTitle, leftContent, m.list.Width+paneChromeW, m.detail.Height+paneChromeH, !m.focusDiff && !m.focusExplorer)
			body := lipgloss.JoinHorizontal(lipgloss.Top, left, m.renderReviewPane())
			view = lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
		}
	}
	if m.reviewSubmitEvent != "" {
		return overlayPopup(view, m.renderReviewSubmitPopup(), m.w)
	}
	if m.localEditMode != noLocalEdit {
		return overlayPopup(view, m.renderLocalEditorPopup(), m.w)
	}
	if m.statusPR.Number > 0 {
		return overlayPopup(view, m.renderPRStatusPopup(), m.w)
	}
	if m.viewManager {
		return overlayPopup(view, m.renderViewManagerPopup(), m.w)
	}
	if m.localDeleteTarget != "" {
		return overlayPopup(view, m.renderLocalDeletePopup(), m.w)
	}
	if m.remoteDeleteID > 0 {
		return overlayPopup(view, m.renderRemoteDeletePopup(), m.w)
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
	if width != m.w {
		block = lipgloss.JoinHorizontal(lipgloss.Top, renderLogo(), block)
	}
	return m.withVersion(block)
}

func (m Model) versionLabel() string {
	if m.version == "" {
		return ""
	}
	label := m.version
	if label != "dev" && !strings.HasPrefix(label, "v") {
		label = "v" + label
	}
	return label
}

func (m Model) withVersion(block string) string {
	label := m.versionLabel()
	if label == "" || m.w <= 0 {
		return block
	}
	version := stMuted.Render(label)
	vw := lipgloss.Width(version)
	lines := strings.Split(block, "\n")
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	} else {
		lines = []string{""}
	}
	available := max(1, m.w-vw)
	first = ansi.Truncate(first, available, "…")
	if pad := m.w - lipgloss.Width(first) - vw; pad > 0 {
		first += strings.Repeat(" ", pad)
	}
	lines[0] = first + version
	return strings.Join(lines, "\n")
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
		state := strings.ToLower(m.cache.PR.State)
		if m.cache.PR.IsDraft {
			state = "draft"
		}
		badgeText, badgeColor = fmt.Sprintf("⇄ #%d %s", m.cache.PR.Number, state), prStateBadgeColor(state)
	}
	badge := lipgloss.NewStyle().
		Background(lipgloss.Color(badgeColor)).Foreground(lipgloss.Color("#ffffff")).
		Padding(0, 1).Render(badgeText)
	title := m.title
	if m.cache.PR != nil {
		title = m.cache.PR.Title
	}
	l1 := badge + "  " + stBold.Render(title)
	stats := m.detailStats()
	scope := fmt.Sprintf("%d files", stats.Files) + " " + stGreenF.Render(fmt.Sprintf("+%d", stats.Additions)) + " " + stRedF.Render(fmt.Sprintf("-%d", stats.Deletions))
	if m.reviewSHA != "" {
		scope = "commit " + m.reviewSHA
	}
	dirty := ""
	if !m.remote && m.workingTreeDirty {
		dirty = "   " + stAttention.Render("● uncommitted changes")
	}
	readiness := ""
	if m.cache.PR != nil || !m.remote {
		readiness = stMuted.Render(fmt.Sprintf("   · %d behind", m.mergeReadiness.Behind))
		if conflicts := len(m.mergeReadiness.ConflictFiles); conflicts > 0 {
			readiness += "   " + stRedF.Render(fmt.Sprintf("⚠ %d conflict files", conflicts))
		} else if m.mergeReadinessErr == nil {
			readiness += "   " + stGreenF.Render("✓ no conflicts")
		} else {
			readiness += "   " + stMuted.Render("merge readiness unavailable")
		}
	}
	l2 := stMuted.Render("⎇ ") + m.baseBranchStyle(m.base).Render(m.base) + stMuted.Render(" ← ") + stFg.Render(m.head) + stMuted.Render("   · ") + scope + dirty + readiness
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
	case m.focusDiff, m.focusExplorer:
		return "REVIEW"
	case m.active == commitsTab:
		return "COMMITS"
	case m.active == conflictsTab:
		return "CONFLICTS"
	case m.active == checksTab:
		return "CHECKS"
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
	// While work is in flight the progress line wins over a lingering
	// notice, so a reload after "Checked out PR #N" is visibly running.
	if m.notice != "" && !m.isLoading() {
		return stGreenF.Render(m.notice) + "  " + m.help.View(m.keys)
	}
	if m.focusDiff {
		hint := stMuted.Render("Review focused · Tab conversation · Shift+Tab full width · q back")
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

// selectionBgOpen returns the opening SGR that paints the selected background,
// or "" when the renderer has color disabled.
func selectionBgOpen() string {
	s := lipgloss.NewStyle().Background(lipgloss.Color(cSelectedBg)).Render("\x00")
	open, _, _ := strings.Cut(s, "\x00")
	return open
}

// highlightSelectedBg paints a full-width selected background across a line,
// re-applying the background after each reset so pre-styled segments keep it.
func highlightSelectedBg(line string, width int) string {
	open := selectionBgOpen()
	if open == "" {
		return line
	}
	line = ansi.Truncate(line, width, "…")
	if gap := width - lipgloss.Width(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return open + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+open) + "\x1b[0m"
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

func copyToClipboard(text string) error { return clipboard.Write(text) }

func shortTS(ts string) string { return strings.Replace(ts, "T", " ", 1) }

func first(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
