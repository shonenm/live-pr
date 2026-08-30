// Asynchronous tea.Cmd constructors that run work off the Update goroutine.
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

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/clipboard"
	"github.com/shonenm/live-pr/internal/debugtime"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prfilter"
	"github.com/shonenm/live-pr/internal/prtemplate"
	"github.com/shonenm/live-pr/internal/richcontent"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
)

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
	commits, commitErr := git.Commits(diffBase)
	files, fileErr := git.ChangedFilesRange(diffBase, "")
	stats, _ := git.DiffStats(diffBase, "")
	if len(files) > stats.Files {
		stats.Files = len(files)
	}
	localHeadOID, headErr := git.Revision("HEAD")
	localState, stateErr := git.CurrentLocalState()
	publishedCommits, localDiverged := 0, false
	revisionRelation := git.RevisionUnknown
	if cache.PR != nil && cache.PR.HeadRefOID != "" {
		revisionRelation, _ = git.CompareRevisions("HEAD", cache.PR.HeadRefOID)
		if revisionRelation == git.RevisionSynced || revisionRelation == git.RevisionLocalAhead {
			if localOnly, err := git.CommitsRange(cache.PR.HeadRefOID, "HEAD"); err == nil {
				publishedCommits = len(commits) - len(localOnly)
				if publishedCommits < 0 {
					publishedCommits = 0
				}
			}
		} else if revisionRelation == git.RevisionDiverged {
			localDiverged = true
		}
	}
	worktree, dirtyErr := git.WorktreeStatus()
	dirty := worktree.Total() > 0
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
		localHeadOID:      localHeadOID,
		localFingerprint:  localState.Fingerprint,
		revisionRelation:  revisionRelation,
		publishedCommits:  publishedCommits,
		localDiverged:     localDiverged,
		dirty:             dirty,
		worktree:          worktree,
		incomplete:        commitErr != nil || fileErr != nil || dirtyErr != nil || headErr != nil || stateErr != nil,
		conclusion:        string(conclusion),
		mergeReadiness:    mergeReadiness,
		mergeReadinessErr: mergeReadinessErr,
	}, nil
}

func fetchPRList(client githubClient, generation uint64, key, query, cursor string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		page, err := client.SearchPRs(query, cursor)
		return prListRefreshed{generation: generation, key: key, appendPage: appendPage, page: page, err: err}
	}
}

// fetchCurrentBranchPRState re-checks the branch's PR purely to refresh what
// the list believes about it.
func fetchCurrentBranchPRState(client githubClient, head string) tea.Cmd {
	return func() tea.Msg {
		msg, _ := fetchCurrentBranchPR(client, head)().(currentBranchPRLoaded)
		msg.stateOnly = true
		return msg
	}
}

func fetchCurrentBranchPR(client githubClient, head string) tea.Cmd {
	return func() tea.Msg {
		pr, err := client.FindForHead(head)
		return currentBranchPRLoaded{pr: pr, err: err}
	}
}

func fetchPRPreview(client githubClient, number int, generation uint64) tea.Cmd {
	return func() tea.Msg {
		pr, err := client.FindPreview(number)
		return prPreviewLoaded{generation: generation, number: number, pr: pr, err: err}
	}
}

func fetchGitHub(client githubClient, head string, number int, generation uint64, prev gh.PRDetail) tea.Cmd {
	return func() tea.Msg {
		if number == 0 {
			pr, err := client.FindForHead(head)
			if err != nil {
				return githubRefreshed{generation: generation, err: err}
			}
			number = pr.Number
		}
		detail := client.LoadLocalPRDetail(number, prev)
		return githubRefreshed{generation: generation, pr: detail.PR, comments: detail.Comments, activities: detail.Activities, reviews: detail.Reviews, reviewComments: detail.ReviewComments, err: detail.PreviewErr, commentsErr: detail.CommentsErr, activitiesErr: detail.ActivitiesErr, reviewsErr: detail.ReviewsErr, reviewCommentsErr: detail.ReviewCommentsErr}
	}
}

const localPollInterval = 2 * time.Second

func scheduleLocalPoll(generation uint64) tea.Cmd {
	return tea.Tick(localPollInterval, func(time.Time) tea.Msg { return localPollTick{generation: generation} })
}

func pollLocalState(generation uint64) tea.Cmd {
	return func() tea.Msg {
		state, err := git.CurrentLocalState()
		return localStatePolled{generation: generation, state: state, err: err}
	}
}

func (m Model) nextLocalPoll() tea.Cmd {
	if m.screen == detailScreen && !m.remote {
		return scheduleLocalPoll(m.targetGeneration)
	}
	return nil
}

func ciPollDelay(failures int) time.Duration {
	return 15 * time.Second * time.Duration(1<<min(max(failures, 0), 3))
}

func scheduleCIPoll(generation uint64, number, failures int) tea.Cmd {
	delay := ciPollDelay(failures)
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return ciPollTick{generation: generation, number: number}
	})
}

func (m Model) nextCIPoll() tea.Cmd {
	if m.screen == detailScreen && m.cache.PR != nil && m.detailMode() == modeLive && (m.cache.PR.State == "" || strings.EqualFold(m.cache.PR.State, "OPEN")) {
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
	return prfilter.CIHealth(pr) == "pending"
}

func pollCI(client githubClient, generation uint64, number int) tea.Cmd {
	return func() tea.Msg {
		pr, err := client.FindChecks(number)
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

// dispatchRichContent dispatches mermaid rendering and avatar resolution only
// when the content key (bodies + width) changed since the last dispatch:
// refreshes with unchanged conversations re-rendered every diagram and
// re-downloaded every avatar otherwise. It records the dispatched key on
// m.detailView.lastRichContentKey — a mutation, not a pure Cmd builder — so
// callers must keep the receiver they invoked it on.
func (m *Model) dispatchRichContent() tea.Cmd {
	width := m.list.Width() - 7
	if width <= 0 {
		// Init can run before the first WindowSizeMsg; rendering mermaid at a
		// negative width wastes the work and caches garbage.
		return nil
	}
	key := richContentKey(width, m.cache.PR, m.cache.Comments, m.cache.Activities)
	if key == m.detailView.lastRichContentKey {
		return nil
	}
	m.detailView.lastRichContentKey = key
	resolved := make(map[string]bool, len(m.avatarColors))
	for login := range m.avatarColors {
		resolved[login] = true
	}
	return loadRichContent(width, m.cache.PR, m.cache.Comments, m.cache.Activities, resolved)
}

func loadRichContent(width int, pr *gh.PR, comments []gh.Comment, activities []gh.Activity, resolved map[string]bool) tea.Cmd {
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
				// Bodies without mermaid come back unchanged: storing them
				// would key the map by their full text for a value that is
				// the same text again. richBody falls back to the raw body
				// on a miss, so only rewritten bodies are kept.
				if replaced := richcontent.ReplaceMermaid(body, rendered); replaced != body {
					results[body] = replaced
				}
			}
			return richBodiesLoaded{key: key, bodies: results}
		},
		func() tea.Msg {
			return avatarColorsLoaded{key: key, colors: loadAvatarColors(avatars)}
		},
	)
}

func fetchRemotePR(client githubClient, pr gh.PR, generation uint64, prev gh.PRDetail) tea.Cmd {
	return func() tea.Msg {
		var headRef, headOID string
		var comments []gh.Comment
		var activities []gh.Activity
		var reviews []gh.Review
		var reviewComments []gh.ReviewThreadComment
		var refErr, previewErr, commentsErr, activitiesErr, reviewsErr, reviewCommentsErr, readinessErr error
		var readiness git.MergeReadiness
		number, base := pr.Number, pr.BaseRefName
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			headRef, headOID, refErr = git.FetchPull(number, base)
		}()
		go func() {
			defer wg.Done()
			detail := client.LoadPRDetail(number, prev)
			comments, activities = detail.Comments, detail.Activities
			reviews, reviewComments = detail.Reviews, detail.ReviewComments
			previewErr, commentsErr, activitiesErr = detail.PreviewErr, detail.CommentsErr, detail.ActivitiesErr
			reviewsErr, reviewCommentsErr = detail.ReviewsErr, detail.ReviewCommentsErr
			if previewErr == nil {
				pr = detail.PR
			}
		}()
		wg.Wait()
		if refErr == nil {
			pr.HeadRefOID = headOID
		}
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
