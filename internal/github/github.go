// Package github wraps the GitHub CLI operations live-pr needs.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	PR             PR
	Comments       []Comment
	Activities     []Activity
	Reviews        []Review
	ReviewComments []ReviewThreadComment
	PreviewErr     error
	CommentsErr    error
	ActivitiesErr  error
	ReviewsErr     error
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

type prListNode struct {
	Number            int    `json:"number"`
	URL               string `json:"url"`
	Title             string `json:"title"`
	State             string `json:"state"`
	BaseRefName       string `json:"baseRefName"`
	BaseRefOID        string `json:"baseRefOid"`
	HeadRefName       string `json:"headRefName"`
	HeadRefOID        string `json:"headRefOid"`
	IsDraft           bool   `json:"isDraft"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	Mergeable         string `json:"mergeable"`
	MergeStateStatus  string `json:"mergeStateStatus"`
	ReviewDecision    string `json:"reviewDecision"`
	UpdatedAt         string `json:"updatedAt"`
	CreatedAt         string `json:"createdAt"`
	Author            PRUser `json:"author"`
	Assignees         struct {
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
	pr := PR{
		Number: node.Number, URL: node.URL, Title: node.Title, State: node.State,
		BaseRefName: node.BaseRefName, BaseRefOID: node.BaseRefOID, HeadRefName: node.HeadRefName, HeadRefOID: node.HeadRefOID,
		IsDraft: node.IsDraft, IsCrossRepository: node.IsCrossRepository,
		Mergeable: node.Mergeable, MergeStateStatus: node.MergeStateStatus, ReviewDecision: node.ReviewDecision,
		UpdatedAt: node.UpdatedAt, CreatedAt: node.CreatedAt, Author: node.Author,
		Assignees: node.Assignees.Nodes, Labels: node.Labels.Nodes,
		ViewerReviewRequested: viewerReviewRequested, CheckRollupState: node.StatusCheckRollup.State,
	}
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

// Client runs GitHub operations through gh.
type Client struct {
	run  runner
	repo *repositoryIdentity
}

// New returns a GitHub CLI client.
func New() Client {
	return Client{repo: sharedRepositoryIdentity, run: func(args ...string) ([]byte, error) {
		if done := debugtime.Start("github gh " + args[0]); done != nil {
			defer done()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	}}
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

// FindOpen finds the open PR for head. Operational errors are returned as-is;
// only a successful empty list becomes ErrPRNotFound.
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
func (c Client) FindChecks(number int) (PR, error) {
	const fields = "number,headRefOid,statusCheckRollup"
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json", fields)
	if err != nil {
		return PR{}, commandError("gh pr view checks", out, err)
	}
	var result struct {
		Number     int       `json:"number"`
		HeadRefOID string    `json:"headRefOid"`
		Checks     []PRCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return PR{}, fmt.Errorf("decode gh PR checks: %w", err)
	}
	return PR{Number: result.Number, HeadRefOID: result.HeadRefOID, Checks: result.Checks, PreviewLoaded: true}, nil
}

// FindPreview loads the expensive preview fields for one PR only.
func (c Client) FindPreview(number int) (PR, error) {
	const fields = "number,url,title,body,state,baseRefName,baseRefOid,headRefName,headRefOid,isDraft,isCrossRepository,mergeable,mergeStateStatus,reviewDecision,additions,deletions,changedFiles,updatedAt,createdAt,author,assignees,labels,reviewRequests,comments,statusCheckRollup"
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json", fields)
	if err != nil {
		return PR{}, commandError("gh pr view", out, err)
	}
	var preview PR
	if err := json.Unmarshal(out, &preview); err != nil {
		return PR{}, fmt.Errorf("decode gh pr preview: %w", err)
	}
	preview.CommentCount = len(preview.Conversation)
	commits, err := c.commitStatusRollups(number)
	if err != nil {
		return PR{}, err
	}
	preview.CommitCount, preview.Commits = len(commits), commits
	preview.PreviewLoaded = true
	return preview, nil
}

func (c Client) commitStatusRollups(number int) ([]PRCommit, error) {
	repoName, err := c.repositoryName()
	if err != nil {
		return nil, err
	}
	owner, name, ok := strings.Cut(repoName, "/")
	if !ok {
		return nil, fmt.Errorf("invalid repository %q", repoName)
	}
	const query = `query($owner:String!,$name:String!,$number:Int!,$after:String){repository(owner:$owner,name:$name){pullRequest(number:$number){commits(first:100,after:$after){nodes{commit{oid committedDate messageHeadline statusCheckRollup{state}}} pageInfo{hasNextPage endCursor}}}}}`
	var commits []PRCommit
	after := ""
	for {
		args := []string{"api", "graphql", "-F", "owner=" + owner, "-F", "name=" + name, "-F", fmt.Sprintf("number=%d", number)}
		if after != "" {
			args = append(args, "-F", "after="+after)
		}
		args = append(args, "-f", "query="+query)
		out, err := c.run(args...)
		if err != nil {
			return nil, commandError("gh api graphql commit statuses", out, err)
		}
		var page struct {
			Data struct {
				Repository struct {
					PullRequest struct {
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
			return nil, fmt.Errorf("decode PR commit statuses: %w", err)
		}
		if len(page.Errors) > 0 {
			return nil, fmt.Errorf("query PR commit statuses: %s", page.Errors[0].Message)
		}
		connection := page.Data.Repository.PullRequest.Commits
		for _, node := range connection.Nodes {
			commits = append(commits, PRCommit{OID: node.Commit.OID, CommittedDate: node.Commit.CommittedDate, MessageHeadline: node.Commit.MessageHeadline, CheckRollupState: node.Commit.StatusCheckRollup.State})
		}
		if !connection.PageInfo.HasNextPage {
			return commits, nil
		}
		after = connection.PageInfo.EndCursor
		if after == "" {
			return nil, errors.New("query PR commit statuses: missing next-page cursor")
		}
	}
}

// IssueComments returns every top-level Conversation comment for a PR.
func (c Client) IssueComments(number int) ([]Comment, error) {
	endpoint := fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments?per_page=100", number)
	out, err := c.run("api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, commandError("gh api issue comments", out, err)
	}
	var pages [][]Comment
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("decode issue comments: %w", err)
	}
	var comments []Comment
	for _, page := range pages {
		comments = append(comments, page...)
	}
	return comments, nil
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
	endpoint := fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/reviews?per_page=100", number)
	out, err := c.run("api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, commandError("gh api pull reviews", out, err)
	}
	var pages [][]Review
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("decode reviews: %w", err)
	}
	var reviews []Review
	for _, page := range pages {
		reviews = append(reviews, page...)
	}
	return reviews, nil
}

// ReviewComments returns the inline review comments for a pull request.
func (c Client) ReviewComments(number int) ([]ReviewThreadComment, error) {
	endpoint := fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/comments?per_page=100", number)
	out, err := c.run("api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, commandError("gh api pull review comments", out, err)
	}
	var pages [][]ReviewThreadComment
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("decode review comments: %w", err)
	}
	var comments []ReviewThreadComment
	for _, page := range pages {
		comments = append(comments, page...)
	}
	return comments, nil
}

// IssueActivities returns non-comment activity from the PR timeline.
func (c Client) IssueActivities(number int) ([]Activity, error) {
	endpoint := fmt.Sprintf("repos/{owner}/{repo}/issues/%d/events?per_page=100", number)
	out, err := c.run("api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, commandError("gh api issue events", out, err)
	}
	var pages [][]Activity
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("decode issue events: %w", err)
	}
	var activities []Activity
	for _, page := range pages {
		activities = append(activities, page...)
	}
	return activities, nil
}

// IssueDetailResult bundles the concurrently-loaded comment and activity
// collections with their independent errors.
type IssueDetailResult struct {
	Comments      []Comment
	Activities    []Activity
	CommentsErr   error
	ActivitiesErr error
}

// IssueDetail loads independent comment and activity collections concurrently.
func (c Client) IssueDetail(number int) IssueDetailResult {
	var r IssueDetailResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.Comments, r.CommentsErr = c.IssueComments(number)
	}()
	go func() {
		defer wg.Done()
		r.Activities, r.ActivitiesErr = c.IssueActivities(number)
	}()
	wg.Wait()
	return r
}

// LoadPRDetail loads preview metadata, comments, and activity concurrently.
func (c Client) LoadPRDetail(number int) PRDetail {
	var detail PRDetail
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		detail.PR, detail.PreviewErr = c.FindPreview(number)
	}()
	go func() {
		defer wg.Done()
		d := c.IssueDetail(number)
		detail.Comments, detail.Activities = d.Comments, d.Activities
		detail.CommentsErr, detail.ActivitiesErr = d.CommentsErr, d.ActivitiesErr
	}()
	go func() {
		defer wg.Done()
		var rwg sync.WaitGroup
		rwg.Add(2)
		go func() { defer rwg.Done(); detail.Reviews, detail.ReviewsErr = c.Reviews(number) }()
		go func() { defer rwg.Done(); detail.ReviewComments, _ = c.ReviewComments(number) }()
		rwg.Wait()
	}()
	wg.Wait()
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
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode pull request review: %w", err)
	}
	file, err := os.CreateTemp("", "live-pr-review-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	out, err := c.run("api", "--method", "POST", fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/reviews", draft.PR), "--input", name)
	if err != nil {
		return commandError("gh api submit pull request review", out, err)
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

// writeCommentBody sends {"body": ...} to a comment endpoint via a temp file so
// arbitrary comment text (newlines, quotes) is passed safely.
func (c Client) writeCommentBody(method, endpoint, body, label string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("comment body must not be empty")
	}
	payload, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: body})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "live-pr-comment-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	out, err := c.run("api", "--method", method, endpoint, "--input", name)
	if err != nil {
		return commandError(label, out, err)
	}
	return nil
}

// Merge merges a pull request with a merge commit.
func (c Client) Merge(number int, headOID string) error {
	if headOID == "" {
		return errors.New("merge requires the reviewed head commit")
	}
	out, err := c.run("pr", "merge", strconv.Itoa(number), "--merge", "--match-head-commit", headOID)
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
		if !pr.IsDraft {
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
	out, err := c.run("pr", "checkout", strconv.Itoa(number))
	if err != nil {
		return commandError("gh pr checkout", out, err)
	}
	return nil
}

// Update replaces the title and body of an existing PR.
func (c Client) Update(head, title, bodyFile string) error {
	out, err := c.run("pr", "edit", head, "--title", title, "--body-file", bodyFile)
	if err != nil {
		return commandError("gh pr edit", out, err)
	}
	return nil
}

// UpdateBody replaces only the body of an existing PR, preserving the title.
func (c Client) UpdateBody(head, bodyFile string) error {
	out, err := c.run("pr", "edit", head, "--body-file", bodyFile)
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
