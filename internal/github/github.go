// Package github wraps the GitHub CLI operations live-pr needs.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shonenm/live-pr/internal/debugtime"
)

// ErrPRNotFound means no open pull request exists for the requested head.
var ErrPRNotFound = errors.New("pull request not found")

// PR is the remote pull-request state needed by live-pr.
type PR struct {
	Number                int                     `json:"number"`
	URL                   string                  `json:"url"`
	Title                 string                  `json:"title"`
	Body                  string                  `json:"body"`
	State                 string                  `json:"state"`
	BaseRefName           string                  `json:"baseRefName,omitempty"`
	BaseRefOID            string                  `json:"baseRefOid,omitempty"`
	HeadRefName           string                  `json:"headRefName,omitempty"`
	HeadRefOID            string                  `json:"headRefOid,omitempty"`
	IsDraft               bool                    `json:"isDraft,omitempty"`
	IsCrossRepository     bool                    `json:"isCrossRepository,omitempty"`
	Mergeable             string                  `json:"mergeable,omitempty"`
	MergeStateStatus      string                  `json:"mergeStateStatus,omitempty"`
	ReviewDecision        string                  `json:"reviewDecision,omitempty"`
	Additions             int                     `json:"additions,omitempty"`
	Deletions             int                     `json:"deletions,omitempty"`
	ChangedFiles          int                     `json:"changedFiles,omitempty"`
	UpdatedAt             string                  `json:"updatedAt,omitempty"`
	Conversation          []PRConversationComment `json:"comments,omitempty"`
	CommentCount          int                     `json:"commentCount,omitempty"`
	CommitCount           int                     `json:"commitCount,omitempty"`
	Commits               []PRCommit              `json:"commits,omitempty"`
	Checks                []PRCheck               `json:"statusCheckRollup,omitempty"`
	CheckRollupState      string                  `json:"checkRollupState,omitempty"`
	Author                PRUser                  `json:"author,omitempty"`
	CreatedAt             string                  `json:"createdAt,omitempty"`
	Assignees             []PRUser                `json:"assignees,omitempty"`
	Labels                []PRLabel               `json:"labels,omitempty"`
	ReviewRequests        []PRUser                `json:"reviewRequests,omitempty"`
	ClosingIssues         []IssueRef              `json:"closingIssues,omitempty"`
	ViewerReviewRequested bool                    `json:"viewerReviewRequested,omitempty"`
	PreviewLoaded         bool                    `json:"previewLoaded,omitempty"`
}

// PRConversationComment is compact list-preview conversation metadata.
type PRConversationComment struct {
	Author    PRUser `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	URL       string `json:"url,omitempty"`
}

// PRCommit is one pull-request commit with its commit-specific CI rollup.
type PRCommit struct {
	OID              string `json:"oid"`
	CommittedDate    string `json:"committedDate,omitempty"`
	MessageHeadline  string `json:"messageHeadline,omitempty"`
	CheckRollupState string `json:"checkRollupState,omitempty"`
}

// PRCheck covers both GitHub check runs and legacy status contexts.
type PRCheck struct {
	Name         string `json:"name,omitempty"`
	Context      string `json:"context,omitempty"`
	Status       string `json:"status,omitempty"`
	Conclusion   string `json:"conclusion,omitempty"`
	State        string `json:"state,omitempty"`
	WorkflowName string `json:"workflowName,omitempty"`
	StartedAt    string `json:"startedAt,omitempty"`
	CompletedAt  string `json:"completedAt,omitempty"`
	DetailsURL   string `json:"detailsUrl,omitempty"`
	TargetURL    string `json:"targetUrl,omitempty"`
}

// URL is the check's log page: a check run's detailsUrl, or a legacy status
// context's targetUrl.
func (c PRCheck) URL() string {
	if c.DetailsURL != "" {
		return c.DetailsURL
	}
	return c.TargetURL
}

// PRUser is a GitHub account attached to PR metadata.
type PRUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

// PRLabel is a GitHub label and its six-digit RGB color.
type PRLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// IssueRef is a linked issue a pull request closes on merge.
type IssueRef struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// Comment is a top-level PR conversation comment.
type Comment struct {
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	HTMLURL   string `json:"html_url"`
	User      struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url,omitempty"`
	} `json:"user"`
}

// PRDetail is one concurrently loaded pull-request detail snapshot.
type PRDetail struct {
	PR                PR
	Comments          []Comment
	Activities        []Activity
	Reviews           []Review
	ReviewComments    []ReviewThreadComment
	PreviewErr        error
	CommentsErr       error
	ActivitiesErr     error
	ReviewsErr        error
	ReviewCommentsErr error
}

// Activity is a non-comment PR timeline event from GitHub's issue events API.
type Activity struct {
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	Event     string `json:"event"`
	CreatedAt string `json:"created_at"`
	CommitID  string `json:"commit_id"`
	Actor     struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url,omitempty"`
	} `json:"actor"`
	Label struct {
		Name string `json:"name"`
	} `json:"label"`
	Assignee struct {
		Login string `json:"login"`
	} `json:"assignee"`
	RequestedReviewer struct {
		Login string `json:"login"`
	} `json:"requested_reviewer"`
	Rename struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"rename"`
}

type runner func(args ...string) ([]byte, error)

type repositoryIdentity struct {
	sync.Mutex
	nameWithOwner string
}

const PRPageSize = 25

// PageInfo is one GitHub cursor boundary.
type PageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	StartCursor string `json:"startCursor"`
	EndCursor   string `json:"endCursor"`
}

// PRPage is one explicitly requested page of lightweight pull-request rows.
type PRPage struct {
	Repository  string
	ViewerLogin string
	PRs         []PR
	TotalCount  int
	PageInfo    PageInfo
}

// prListNode is a GraphQL list row. The scalar leaves decode straight into
// the embedded PR (same json tags); only the GraphQL connection shapes need
// their own fields, which shadow the PR slices of the same name during
// decoding and are copied over in pullRequest.
type prListNode struct {
	PR
	Assignees struct {
		Nodes []PRUser `json:"nodes"`
	} `json:"assignees"`
	Labels struct {
		Nodes []PRLabel `json:"nodes"`
	} `json:"labels"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer PRUser `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	StatusCheckRollup struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
}

func (node prListNode) pullRequest(viewerReviewRequested bool) PR {
	pr := node.PR
	pr.Assignees, pr.Labels = node.Assignees.Nodes, node.Labels.Nodes
	pr.CheckRollupState = node.StatusCheckRollup.State
	pr.ViewerReviewRequested = viewerReviewRequested
	pr.ReviewRequests = nil
	for _, request := range node.ReviewRequests.Nodes {
		if request.RequestedReviewer.Login != "" {
			pr.ReviewRequests = append(pr.ReviewRequests, request.RequestedReviewer)
		}
	}
	return pr
}

// sharedRepositoryIdentity caches the resolved repository name for the whole
// process. live-pr operates within a single repository, so one shared identity
// suffices — and it avoids a cwd-keyed map that would never be evicted.
var sharedRepositoryIdentity = &repositoryIdentity{}

// gh invocation deadlines. Most calls are small JSON round trips, but the
// paginated list endpoints and pr checkout move real data and legitimately
// outlast the default on slow links.
const (
	defaultRunTimeout = 30 * time.Second
	longRunTimeout    = 120 * time.Second
)

// Client runs GitHub operations through gh.
type Client struct {
	run        runner
	runTimeout func(timeout time.Duration, args ...string) ([]byte, error)
	repo       *repositoryIdentity
}

// New returns a GitHub CLI client.
func New() Client {
	run := func(timeout time.Duration, args ...string) ([]byte, error) {
		if done := debugtime.Start("github gh " + args[0]); done != nil {
			defer done()
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		// stdout and stderr must stay separate: gh prints warnings (update
		// notices, deprecations) on stderr, and mixing them into stdout made
		// every JSON decode fail inscrutably.
		cmd := exec.CommandContext(ctx, "gh", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.Bytes(), runError(err, timeout, ctx.Err() == context.DeadlineExceeded, stderr.String())
	}
	return Client{
		repo:       sharedRepositoryIdentity,
		run:        func(args ...string) ([]byte, error) { return run(defaultRunTimeout, args...) },
		runTimeout: run,
	}
}

// runWithTimeout runs gh with an explicit deadline. Test fakes that only
// stub run keep working: without a runTimeout the deadline stays advisory
// and the call goes through the default runner.
func (c Client) runWithTimeout(timeout time.Duration, args ...string) ([]byte, error) {
	if c.runTimeout != nil {
		return c.runTimeout(timeout, args...)
	}
	return c.run(args...)
}

// runError folds the timeout cause and stderr detail into a run failure:
// "signal: killed" alone says nothing, and gh's stderr carries the reason.
func runError(err error, timeout time.Duration, timedOut bool, stderr string) error {
	if err == nil {
		return nil
	}
	if timedOut {
		err = fmt.Errorf("timed out after %s: %w", timeout, err)
	}
	if detail := strings.TrimSpace(stderr); detail != "" {
		err = fmt.Errorf("%w: %s", err, detail)
	}
	return err
}

func (c Client) repositoryName() (string, error) {
	if c.repo != nil {
		c.repo.Lock()
		defer c.repo.Unlock()
		if c.repo.nameWithOwner != "" {
			return c.repo.nameWithOwner, nil
		}
	}
	out, err := c.run("repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return "", commandError("gh repo view", out, err)
	}
	var repo struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal(out, &repo); err != nil {
		return "", fmt.Errorf("decode gh repo view: %w", err)
	}
	if c.repo != nil {
		c.repo.nameWithOwner = repo.NameWithOwner
	}
	return repo.NameWithOwner, nil
}

// prFields is the gh pr view field list shared by the single-PR lookups.
const prFields = "number,url,title,body,state,baseRefName,baseRefOid,headRefName,headRefOid,isDraft,isCrossRepository,author,createdAt,assignees,labels"

func (c Client) Find(number int) (PR, error) {
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json", prFields)
	if err != nil {
		return PR{}, commandError("gh pr view", out, err)
	}
	var pr PR
	if err := json.Unmarshal(out, &pr); err != nil {
		return PR{}, fmt.Errorf("decode gh pr view: %w", err)
	}
	return pr, nil
}

// FindOpen finds the open PR for head. Operational errors are returned as-is;
// only a successful empty list becomes ErrPRNotFound.
func (c Client) FindOpen(head string) (PR, error) {
	return c.findHead(head, "open")
}

// FindForHead prefers an open PR, then returns the newest PR in any state.
// When multiple PRs share the same head branch, the newest (highest number) wins.
func (c Client) FindForHead(head string) (PR, error) {
	prs, err := c.findAllHead(head, "open")
	if err != nil && !errors.Is(err, ErrPRNotFound) {
		return PR{}, err
	}
	if len(prs) == 0 {
		prs, err = c.findAllHead(head, "all")
		if err != nil {
			return PR{}, err
		}
	}
	return newestPR(prs), nil
}

// FindAllForHead returns every PR (any state) whose head is the given branch.
func (c Client) FindAllForHead(head string) ([]PR, error) {
	return c.findAllHead(head, "all")
}

func (c Client) findAllHead(head, state string) ([]PR, error) {
	out, err := c.run("pr", "list", "--head", head, "--state", state, "--json", prFields)
	if err != nil {
		return nil, commandError("gh pr list", out, err)
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("decode gh pr list: %w", err)
	}
	if len(prs) == 0 {
		return nil, ErrPRNotFound
	}
	return prs, nil
}

func newestPR(prs []PR) PR {
	best := prs[0]
	for _, pr := range prs[1:] {
		if pr.Number > best.Number {
			best = pr
		}
	}
	return best
}

func (c Client) findHead(head, state string) (PR, error) {
	prs, err := c.findAllHead(head, state)
	if err != nil {
		return PR{}, err
	}
	return newestPR(prs), nil
}

// SearchPRs returns exactly one page. Callers decide when to request PageInfo.EndCursor.
func (c Client) SearchPRs(query, cursor string) (PRPage, error) {
	repoName, err := c.repositoryName()
	if err != nil {
		return PRPage{}, err
	}
	query = strings.TrimSpace("repo:" + repoName + " is:pr " + query + " sort:updated-desc")
	graphqlQuery := `query($searchQuery:String!,$pageSize:Int!,$after:String){viewer{login avatarUrl} search(query:$searchQuery,type:ISSUE,first:$pageSize,after:$after){issueCount nodes{... on PullRequest{number url title state baseRefName baseRefOid headRefName headRefOid isDraft isCrossRepository mergeable mergeStateStatus reviewDecision updatedAt createdAt author{login avatarUrl} assignees(first:10){nodes{login avatarUrl}} reviewRequests(first:20){nodes{requestedReviewer{... on User{login avatarUrl}}}} labels(first:20){nodes{name color}} statusCheckRollup{state}}} pageInfo{hasNextPage startCursor endCursor}}}`
	args := []string{"api", "graphql", "-F", "searchQuery=" + query, "-F", fmt.Sprintf("pageSize=%d", PRPageSize)}
	if cursor != "" {
		args = append(args, "-F", "after="+cursor)
	}
	args = append(args, "-f", "query="+graphqlQuery)
	out, err := c.run(args...)
	if err != nil {
		return PRPage{}, commandError("gh api graphql", out, err)
	}
	var response struct {
		Data struct {
			Viewer PRUser `json:"viewer"`
			Search struct {
				IssueCount int          `json:"issueCount"`
				Nodes      []prListNode `json:"nodes"`
				PageInfo   PageInfo     `json:"pageInfo"`
			} `json:"search"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return PRPage{}, fmt.Errorf("decode PR search page: %w", err)
	}
	if len(response.Errors) > 0 {
		return PRPage{}, fmt.Errorf("query PR search page: %s", response.Errors[0].Message)
	}
	if response.Data.Search.PageInfo.HasNextPage {
		if response.Data.Search.PageInfo.EndCursor == "" {
			return PRPage{}, errors.New("query PR search page: missing next-page cursor")
		}
		if response.Data.Search.PageInfo.EndCursor == cursor {
			return PRPage{}, errors.New("query PR search page: cursor did not advance")
		}
	}
	prs := make([]PR, 0, len(response.Data.Search.Nodes))
	for _, node := range response.Data.Search.Nodes {
		prs = append(prs, node.pullRequest(false))
	}
	return PRPage{Repository: repoName, ViewerLogin: response.Data.Viewer.Login, PRs: prs, TotalCount: response.Data.Search.IssueCount, PageInfo: response.Data.Search.PageInfo}, nil
}

// FindChecks loads only the current head revision and its CI rollup.
// FindChecks polls the CI state. It also carries the pull request state:
// this call outlives a merge, and the poller needs to know when to stop.
func (c Client) FindChecks(number int) (PR, error) {
	const fields = "number,state,isDraft,headRefOid,statusCheckRollup"
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json", fields)
	if err != nil {
		return PR{}, commandError("gh pr view checks", out, err)
	}
	var result struct {
		Number     int       `json:"number"`
		State      string    `json:"state"`
		IsDraft    bool      `json:"isDraft"`
		HeadRefOID string    `json:"headRefOid"`
		Checks     []PRCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return PR{}, fmt.Errorf("decode gh PR checks: %w", err)
	}
	return PR{Number: result.Number, State: result.State, IsDraft: result.IsDraft, HeadRefOID: result.HeadRefOID, Checks: result.Checks, PreviewLoaded: true}, nil
}

// FindPreview loads the expensive preview fields for one remote PR.
func (c Client) FindPreview(number int) (PR, error) { return c.findPreview(number, false) }

// FindLocalPreview omits diff statistics supplied authoritatively by local Git.
func (c Client) FindLocalPreview(number int) (PR, error) { return c.findPreview(number, true) }

func (c Client) findPreview(number int, local bool) (PR, error) {
	fields := "number,url,title,body,state,baseRefName,baseRefOid,headRefName,headRefOid,isDraft,isCrossRepository,mergeable,mergeStateStatus,reviewDecision,updatedAt,createdAt,author,assignees,labels,reviewRequests,comments,statusCheckRollup"
	if !local {
		fields += ",additions,deletions,changedFiles"
	}
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json", fields)
	if err != nil {
		return PR{}, commandError("gh pr view", out, err)
	}
	var preview PR
	if err := json.Unmarshal(out, &preview); err != nil {
		return PR{}, fmt.Errorf("decode gh pr preview: %w", err)
	}
	preview.CommentCount = len(preview.Conversation)
	// A failed rollup fetch only costs the per-commit CI states and the
	// linked issues; discarding the whole preview over it left the PR with
	// no metadata at all.
	if commits, issues, err := c.loadCommitStatusRollups(number, local); err == nil {
		preview.CommitCount, preview.Commits = len(commits), commits
		preview.ClosingIssues = issues
	}
	preview.PreviewLoaded = true
	return preview, nil
}

// commitStatusRollups loads the per-commit CI rollups and the issues the PR
// closes. The linked issues ride along here because this is already the
// preview's per-PR GraphQL round trip, and gh pr view --json cannot return
// the issue titles.
func (c Client) commitStatusRollups(number int) ([]PRCommit, []IssueRef, error) {
	return c.loadCommitStatusRollups(number, false)
}

func (c Client) loadCommitStatusRollups(number int, local bool) ([]PRCommit, []IssueRef, error) {
	repoName, err := c.repositoryName()
	if err != nil {
		return nil, nil, err
	}
	owner, name, ok := strings.Cut(repoName, "/")
	if !ok {
		return nil, nil, fmt.Errorf("invalid repository %q", repoName)
	}
	query := `query($owner:String!,$name:String!,$number:Int!,$after:String){repository(owner:$owner,name:$name){pullRequest(number:$number){closingIssuesReferences(first:10){nodes{number title}} commits(first:100,after:$after){nodes{commit{oid statusCheckRollup{state}}} pageInfo{hasNextPage endCursor}}}}}`
	if !local {
		query = `query($owner:String!,$name:String!,$number:Int!,$after:String){repository(owner:$owner,name:$name){pullRequest(number:$number){closingIssuesReferences(first:10){nodes{number title}} commits(first:100,after:$after){nodes{commit{oid committedDate messageHeadline statusCheckRollup{state}}} pageInfo{hasNextPage endCursor}}}}}`
	}
	var commits []PRCommit
	var issues []IssueRef
	after := ""
	for {
		args := []string{"api", "graphql", "-F", "owner=" + owner, "-F", "name=" + name, "-F", fmt.Sprintf("number=%d", number)}
		if after != "" {
			args = append(args, "-F", "after="+after)
		}
		args = append(args, "-f", "query="+query)
		out, err := c.run(args...)
		if err != nil {
			return nil, nil, commandError("gh api graphql commit statuses", out, err)
		}
		var page struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ClosingIssuesReferences struct {
							Nodes []IssueRef `json:"nodes"`
						} `json:"closingIssuesReferences"`
						Commits struct {
							Nodes []struct {
								Commit struct {
									OID               string `json:"oid"`
									CommittedDate     string `json:"committedDate"`
									MessageHeadline   string `json:"messageHeadline"`
									StatusCheckRollup struct {
										State string `json:"state"`
									} `json:"statusCheckRollup"`
								} `json:"commit"`
							} `json:"nodes"`
							PageInfo PageInfo `json:"pageInfo"`
						} `json:"commits"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(out, &page); err != nil {
			return nil, nil, fmt.Errorf("decode PR commit statuses: %w", err)
		}
		if len(page.Errors) > 0 {
			return nil, nil, fmt.Errorf("query PR commit statuses: %s", page.Errors[0].Message)
		}
		if after == "" {
			// Every commit page repeats the closing references; the first
			// page's copy is the whole list.
			issues = page.Data.Repository.PullRequest.ClosingIssuesReferences.Nodes
		}
		connection := page.Data.Repository.PullRequest.Commits
		for _, node := range connection.Nodes {
			commits = append(commits, PRCommit{OID: node.Commit.OID, CommittedDate: node.Commit.CommittedDate, MessageHeadline: node.Commit.MessageHeadline, CheckRollupState: node.Commit.StatusCheckRollup.State})
		}
		if !connection.PageInfo.HasNextPage {
			return commits, issues, nil
		}
		after = connection.PageInfo.EndCursor
		if after == "" {
			return nil, nil, errors.New("query PR commit statuses: missing next-page cursor")
		}
	}
}

// paginatedList runs a --paginate --slurp endpoint and flattens the pages.
func paginatedList[T any](c Client, endpoint, label string) ([]T, error) {
	out, err := c.runWithTimeout(longRunTimeout, "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, commandError(label, out, err)
	}
	var pages [][]T
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	var items []T
	for _, page := range pages {
		items = append(items, page...)
	}
	return items, nil
}

// IssueComments returns every top-level Conversation comment for a PR.
func (c Client) IssueComments(number int) ([]Comment, error) {
	return paginatedList[Comment](c, fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments?per_page=100", number), "gh api issue comments")
}

// issueCommentsSince returns only the comments created or updated at or
// after since, an RFC3339 timestamp previously reported by the server.
func (c Client) issueCommentsSince(number int, since string) ([]Comment, error) {
	return paginatedList[Comment](c, fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments?per_page=100&since=%s", number, url.QueryEscape(since)), "gh api issue comments")
}

// latestCommentUpdate returns the newest updated_at among comments. It
// reports false when any timestamp is missing or unparseable — a cache that
// cannot prove its own freshness must not seed an incremental fetch.
func latestCommentUpdate(comments []Comment) (string, bool) {
	var latest time.Time
	var raw string
	for _, comment := range comments {
		ts, err := time.Parse(time.RFC3339, comment.UpdatedAt)
		if err != nil {
			return "", false
		}
		if ts.After(latest) {
			latest, raw = ts, comment.UpdatedAt
		}
	}
	return raw, raw != ""
}

// mergeComments upserts fetched comments into cached by ID and restores the
// list endpoint's ordering (ascending created time, IDs breaking ties).
func mergeComments(cached, fetched []Comment) []Comment {
	merged := make([]Comment, len(cached))
	copy(merged, cached)
	index := make(map[int64]int, len(merged))
	for i, comment := range merged {
		index[comment.ID] = i
	}
	for _, comment := range fetched {
		if i, ok := index[comment.ID]; ok {
			merged[i] = comment
			continue
		}
		index[comment.ID] = len(merged)
		merged = append(merged, comment)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].CreatedAt != merged[j].CreatedAt {
			return merged[i].CreatedAt < merged[j].CreatedAt
		}
		return merged[i].ID < merged[j].ID
	})
	return merged
}

// Review is a submitted pull-request review (approve / request-changes /
// comment) with its optional summary body.
type Review struct {
	ID          int64  `json:"id"`
	Body        string `json:"body"`
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
	User        struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url,omitempty"`
	} `json:"user"`
}

// ReviewThreadComment is one inline review comment on a diff line.
type ReviewThreadComment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	CreatedAt string `json:"created_at"`
	User      struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url,omitempty"`
	} `json:"user"`
}

// Reviews returns the submitted reviews for a pull request.
func (c Client) Reviews(number int) ([]Review, error) {
	return paginatedList[Review](c, fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/reviews?per_page=100", number), "gh api pull reviews")
}

// ReviewComments returns the inline review comments for a pull request.
func (c Client) ReviewComments(number int) ([]ReviewThreadComment, error) {
	return paginatedList[ReviewThreadComment](c, fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/comments?per_page=100", number), "gh api pull review comments")
}

// IssueActivities returns non-comment activity from the PR timeline.
func (c Client) IssueActivities(number int) ([]Activity, error) {
	return paginatedList[Activity](c, fmt.Sprintf("repos/{owner}/{repo}/issues/%d/events?per_page=100", number), "gh api issue events")
}

// LoadPRDetail loads preview metadata, comments, and activity concurrently.
// prev is the caller's cached snapshot of the same PR: when it carries
// comments, only those created or updated since the newest cached updated_at
// are fetched and merged in by ID. An empty prev, a number mismatch, or any
// doubt about the cache loads everything, exactly like a first load.
func (c Client) LoadPRDetail(number int, prev PRDetail) PRDetail {
	return c.loadPRDetail(number, prev, false)
}

// LoadLocalPRDetail keeps remote conversation/review metadata fresh while
// leaving diff, file, and commit content to the checked-out repository.
func (c Client) LoadLocalPRDetail(number int, prev PRDetail) PRDetail {
	return c.loadPRDetail(number, prev, true)
}

func (c Client) loadPRDetail(number int, prev PRDetail, local bool) PRDetail {
	since, incremental := "", false
	if number != 0 && prev.PR.Number == number && len(prev.Comments) > 0 {
		since, incremental = latestCommentUpdate(prev.Comments)
	}
	var detail PRDetail
	merged := false
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		if local {
			detail.PR, detail.PreviewErr = c.FindLocalPreview(number)
		} else {
			detail.PR, detail.PreviewErr = c.FindPreview(number)
		}
	}()
	go func() {
		defer wg.Done()
		if incremental {
			if fetched, err := c.issueCommentsSince(number, since); err == nil {
				detail.Comments, merged = mergeComments(prev.Comments, fetched), true
				return
			}
			// An incremental failure must not surface a new error mode; fall
			// through to the same full fetch a first load performs.
		}
		detail.Comments, detail.CommentsErr = c.IssueComments(number)
	}()
	go func() {
		defer wg.Done()
		detail.Activities, detail.ActivitiesErr = c.IssueActivities(number)
	}()
	go func() {
		defer wg.Done()
		var rwg sync.WaitGroup
		rwg.Add(2)
		go func() { defer rwg.Done(); detail.Reviews, detail.ReviewsErr = c.Reviews(number) }()
		go func() { defer rwg.Done(); detail.ReviewComments, detail.ReviewCommentsErr = c.ReviewComments(number) }()
		rwg.Wait()
	}()
	wg.Wait()
	// A remotely deleted comment survives an incremental merge — since= can
	// never report it. The preview fetched in this same call counts the full
	// conversation, so any disagreement (or a failed preview, which leaves no
	// count to check against) discards the merge for a full fetch.
	if merged && (detail.PreviewErr != nil || len(detail.Comments) != detail.PR.CommentCount) {
		detail.Comments, detail.CommentsErr = c.IssueComments(number)
	}
	return detail
}

// SubmitReview publishes a complete review and its inline comments atomically.
func (c Client) SubmitReview(draft ReviewDraft, event ReviewEvent) error {
	if err := ValidateReviewDraft(draft); err != nil {
		return err
	}
	if err := ValidateReviewEvent(event); err != nil {
		return err
	}
	if event == ReviewRequestChangesEvent && strings.TrimSpace(draft.Body) == "" {
		return errors.New("request changes requires a review body")
	}
	if event == ReviewCommentEvent && strings.TrimSpace(draft.Body) == "" && len(draft.Comments) == 0 {
		return errors.New("comment review requires a body or inline comment")
	}
	current, err := c.Find(draft.PR)
	if err != nil {
		return fmt.Errorf("verify pull request review revision: %w", err)
	}
	if current.HeadRefOID != draft.Commit {
		return fmt.Errorf("pull request head changed from %s to %s; review the new revision before submitting", draft.Commit, current.HeadRefOID)
	}
	payload := struct {
		CommitID string          `json:"commit_id"`
		Body     string          `json:"body,omitempty"`
		Event    ReviewEvent     `json:"event"`
		Comments []ReviewComment `json:"comments,omitempty"`
	}{CommitID: draft.Commit, Body: draft.Body, Event: event, Comments: draft.Comments}
	return c.runWithJSONInput("POST", fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/reviews", draft.PR), payload, "gh api submit pull request review")
}

// runWithJSONInput sends a JSON payload through a temp file so arbitrary text
// (newlines, quotes) passes safely to gh api --input.
func (c Client) runWithJSONInput(method, endpoint string, payload any, label string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s: %w", label, err)
	}
	file, err := os.CreateTemp("", "live-pr-input-*.json")
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("%s: %w", label, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	out, err := c.run("api", "--method", method, endpoint, "--input", name)
	if err != nil {
		return commandError(label, out, err)
	}
	return nil
}

// PostIssueComment posts a conversation comment on a pull request.
func (c Client) PostIssueComment(number int, body string) error {
	return c.writeCommentBody("POST", fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments", number), body, "gh api post issue comment")
}

// DeleteIssueComment removes a conversation comment by its ID.
func (c Client) DeleteIssueComment(id int64) error {
	out, err := c.run("api", "--method", "DELETE", fmt.Sprintf("repos/{owner}/{repo}/issues/comments/%d", id))
	if err != nil {
		return commandError("gh api delete issue comment", out, err)
	}
	return nil
}

// EditIssueComment updates an existing conversation comment by its ID.
func (c Client) EditIssueComment(id int64, body string) error {
	return c.writeCommentBody("PATCH", fmt.Sprintf("repos/{owner}/{repo}/issues/comments/%d", id), body, "gh api edit issue comment")
}

// writeCommentBody sends {"body": ...} to a comment endpoint.
func (c Client) writeCommentBody(method, endpoint, body, label string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("comment body must not be empty")
	}
	return c.runWithJSONInput(method, endpoint, struct {
		Body string `json:"body"`
	}{Body: body}, label)
}

// MergeMethod selects how gh lands a pull request.
type MergeMethod string

const (
	MergeCommit MergeMethod = "merge"
	MergeSquash MergeMethod = "squash"
	MergeRebase MergeMethod = "rebase"
)

// Merge merges a pull request with the given method.
func (c Client) Merge(number int, headOID string, method MergeMethod) error {
	if headOID == "" {
		return errors.New("merge requires the reviewed head commit")
	}
	switch method {
	case MergeCommit, MergeSquash, MergeRebase:
	default:
		return fmt.Errorf("unknown merge method %q", method)
	}
	out, err := c.run("pr", "merge", strconv.Itoa(number), "--"+string(method), "--match-head-commit", headOID)
	if err != nil {
		return commandError("gh pr merge", out, err)
	}
	return nil
}

// Close closes a pull request without merging it.
func (c Client) Close(number int) error {
	out, err := c.run("pr", "close", strconv.Itoa(number))
	if err != nil {
		return commandError("gh pr close", out, err)
	}
	return nil
}

// SetStatus changes a pull request to open, closed, or draft.
func (c Client) SetStatus(pr PR, target string) error {
	if strings.EqualFold(pr.State, "MERGED") {
		return errors.New("merged pull requests cannot change status")
	}
	if target == "closed" {
		return c.Close(pr.Number)
	}
	if strings.EqualFold(pr.State, "CLOSED") {
		out, err := c.run("pr", "reopen", strconv.Itoa(pr.Number))
		if err != nil {
			return commandError("gh pr reopen", out, err)
		}
	}
	var args []string
	switch target {
	case "open":
		// Reopening a closed PR must not change its draftness; only the
		// explicit draft -> open transition marks it ready for review.
		if strings.EqualFold(pr.State, "CLOSED") || !pr.IsDraft {
			return nil
		}
		args = []string{"pr", "ready", strconv.Itoa(pr.Number)}
	case "draft":
		if pr.IsDraft && !strings.EqualFold(pr.State, "CLOSED") {
			return nil
		}
		args = []string{"pr", "ready", strconv.Itoa(pr.Number), "--undo"}
	default:
		return fmt.Errorf("unsupported pull request status %q", target)
	}
	out, err := c.run(args...)
	if err != nil {
		return commandError("gh pr status", out, err)
	}
	return nil
}

// Checkout checks out a pull request using GitHub CLI's native branch handling.
func (c Client) Checkout(number int) error {
	out, err := c.runWithTimeout(longRunTimeout, "pr", "checkout", strconv.Itoa(number))
	if err != nil {
		return commandError("gh pr checkout", out, err)
	}
	return nil
}

// Update replaces the title and body of an existing PR. It addresses the PR
// by number: several PRs may share one head branch, and a branch-name edit
// could land on the wrong one.
func (c Client) Update(number int, title, bodyFile string) error {
	out, err := c.run("pr", "edit", strconv.Itoa(number), "--title", title, "--body-file", bodyFile)
	if err != nil {
		return commandError("gh pr edit", out, err)
	}
	return nil
}

// UpdateBody replaces only the body of an existing PR, preserving the title.
func (c Client) UpdateBody(number int, bodyFile string) error {
	out, err := c.run("pr", "edit", strconv.Itoa(number), "--body-file", bodyFile)
	if err != nil {
		return commandError("gh pr edit body", out, err)
	}
	return nil
}

// Create creates a PR and returns gh's output (normally its URL).
func (c Client) Create(base, head, title, bodyFile string, draft bool) (string, error) {
	args := []string{"pr", "create", "--base", base, "--head", head, "--title", title, "--body-file", bodyFile}
	if draft {
		args = append(args, "--draft")
	}
	out, err := c.run(args...)
	if err != nil {
		return "", commandError("gh pr create", out, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func commandError(op string, out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return fmt.Errorf("%s: %w", op, err)
	}
	return fmt.Errorf("%s: %w: %s", op, err, msg)
}

// StatusHint classifies a fetch failure for the status line so the TUI can
// tell setup problems apart from being offline. It returns "" when the
// failure looks like plain network trouble — callers keep their existing
// offline wording — and a short actionable hint otherwise. Deliberately a
// dumb string check over the flat error text runError/commandError build.
func StatusHint(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "gh not installed"
	}
	lower := strings.ToLower(err.Error())
	// gh's unauthenticated hint ("To get started with GitHub CLI, please
	// run:  gh auth login") and expired-token responses (HTTP 401: Bad
	// credentials) all point the same way.
	for _, sign := range []string{"gh auth login", "authentication", "bad credentials", "http 401"} {
		if strings.Contains(lower, sign) {
			return "run gh auth login"
		}
	}
	for _, sign := range []string{"dial tcp", "no such host", "connection refused", "network is unreachable", "i/o timeout", "timed out", "error connecting to"} {
		if strings.Contains(lower, sign) {
			return ""
		}
	}
	// gh failures carry their stderr after the exit status (see runError);
	// surface its first line so a broken custom query names itself.
	if _, detail, ok := strings.Cut(err.Error(), "exit status"); ok {
		if _, detail, ok = strings.Cut(detail, ": "); ok {
			detail, _, _ = strings.Cut(detail, "\n")
			detail = strings.TrimPrefix(strings.TrimSpace(detail), "gh: ")
			if r := []rune(detail); len(r) > 80 {
				detail = strings.TrimSpace(string(r[:79])) + "…"
			}
			if detail != "" {
				return detail
			}
		}
	}
	return ""
}
