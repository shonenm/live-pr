// Messages delivered to Update by the asynchronous commands.
package tui

import (
	"crypto/sha256"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/shonenm/live-pr/internal/store"
)

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

type localPollTick struct{ generation uint64 }

type localStatePolled struct {
	generation uint64
	state      git.LocalState
	err        error
}

type localBranchReloaded struct {
	generation uint64
	next       *Model
	err        error
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
	remoteCommits                        []git.Commit
	files                                []git.ChangedFile
	localHeadOID                         string
	revisionRelation                     git.RevisionRelation
	publishedCommits                     int
	localDiverged                        bool
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
	generation uint64
	number     int
	next       *Model
	err        error
}

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
	key    [sha256.Size]byte
	bodies map[string]string
}

type avatarColorsLoaded struct {
	key    [sha256.Size]byte
	colors map[string]string
}

type listAvatarColorsLoaded struct {
	generation uint64
	colors     map[string]string
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
	remoteCommits     []git.Commit
	files             []git.ChangedFile
	stats             git.ChangeStats
	localHeadOID      string
	localFingerprint  string
	revisionRelation  git.RevisionRelation
	publishedCommits  int
	localDiverged     bool
	dirty             bool
	worktree          git.WorktreeSummary
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
