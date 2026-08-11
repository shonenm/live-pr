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
}

// PRUser is a GitHub account attached to PR metadata.
type PRUser struct {
	Login string `json:"login"`
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
		Login string `json:"login"`
	} `json:"user"`
}

// PRDetail is one concurrently loaded pull-request detail snapshot.
type PRDetail struct {
	PR            PR
	Comments      []Comment
	Activities    []Activity
	PreviewErr    error
	CommentsErr   error
	ActivitiesErr error
}

// Activity is a non-comment PR timeline event from GitHub's issue events API.
type Activity struct {
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	Event     string `json:"event"`
	CreatedAt string `json:"created_at"`
	CommitID  string `json:"commit_id"`
	Actor     struct {
		Login string `json:"login"`
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

var repositoryIdentities sync.Map

// Client runs GitHub operations through gh.
type Client struct {
	run  runner
	repo *repositoryIdentity
}

// New returns a GitHub CLI client.
func New() Client {
	var repo *repositoryIdentity
	if cwd, err := os.Getwd(); err == nil {
		value, _ := repositoryIdentities.LoadOrStore(cwd, &repositoryIdentity{})
		repo = value.(*repositoryIdentity)
	}
	return Client{run: func(args ...string) ([]byte, error) {
		if done := debugtime.Start("github gh " + args[0]); done != nil {
			defer done()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	}, repo: repo}
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
func (c Client) FindForHead(head string) (PR, error) {
	pr, err := c.FindOpen(head)
	if !errors.Is(err, ErrPRNotFound) {
		return pr, err
	}
	return c.findHead(head, "all")
}

func (c Client) findHead(head, state string) (PR, error) {
	out, err := c.run("pr", "list", "--head", head, "--state", state, "--limit", "1", "--json", prFields)
	if err != nil {
		return PR{}, commandError("gh pr list", out, err)
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return PR{}, fmt.Errorf("decode gh pr list: %w", err)
	}
	if len(prs) == 0 {
		return PR{}, ErrPRNotFound
	}
	return prs[0], nil
}

// SearchPRs returns exactly one page. Callers decide when to request PageInfo.EndCursor.
func (c Client) SearchPRs(query, cursor string) (PRPage, error) {
	repoName, err := c.repositoryName()
	if err != nil {
		return PRPage{}, err
	}
	query = strings.TrimSpace("repo:" + repoName + " is:pr " + query + " sort:updated-desc")
	graphqlQuery := `query($searchQuery:String!,$pageSize:Int!,$after:String){viewer{login} search(query:$searchQuery,type:ISSUE,first:$pageSize,after:$after){issueCount nodes{... on PullRequest{number url title state baseRefName baseRefOid headRefName headRefOid isDraft isCrossRepository mergeable mergeStateStatus reviewDecision updatedAt createdAt author{login} assignees(first:10){nodes{login}} reviewRequests(first:20){nodes{requestedReviewer{... on User{login}}}} labels(first:20){nodes{name color}} statusCheckRollup{state}}} pageInfo{hasNextPage startCursor endCursor}}}`
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
	return PRPage{ViewerLogin: response.Data.Viewer.Login, PRs: prs, TotalCount: response.Data.Search.IssueCount, PageInfo: response.Data.Search.PageInfo}, nil
}

// FindPreview loads the expensive preview fields for one PR only.
func (c Client) FindPreview(number int) (PR, error) {
	const fields = "number,url,title,body,state,baseRefName,baseRefOid,headRefName,headRefOid,isDraft,isCrossRepository,mergeable,mergeStateStatus,reviewDecision,additions,deletions,changedFiles,updatedAt,createdAt,author,assignees,labels,reviewRequests,comments,statusCheckRollup"
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json", fields)
	if err != nil {
		return PR{}, commandError("gh pr view", out, err)
	}
	var preview struct {
		PR
		Conversation []PRConversationComment `json:"comments"`
		Checks       []PRCheck               `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &preview); err != nil {
		return PR{}, fmt.Errorf("decode gh pr preview: %w", err)
	}
	preview.PR.Conversation = preview.Conversation
	preview.PR.CommentCount = len(preview.Conversation)
	commits, err := c.commitStatusRollups(number)
	if err != nil {
		return PR{}, err
	}
	preview.PR.CommitCount, preview.PR.Commits = len(commits), commits
	preview.PR.Checks = preview.Checks
	preview.PR.PreviewLoaded = true
	return preview.PR, nil
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

// IssueDetail loads independent comment and activity collections concurrently.
func (c Client) IssueDetail(number int) ([]Comment, []Activity, error, error) {
	var comments []Comment
	var activities []Activity
	var commentsErr, activitiesErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		comments, commentsErr = c.IssueComments(number)
	}()
	go func() {
		defer wg.Done()
		activities, activitiesErr = c.IssueActivities(number)
	}()
	wg.Wait()
	return comments, activities, commentsErr, activitiesErr
}

// LoadPRDetail loads preview metadata, comments, and activity concurrently.
func (c Client) LoadPRDetail(number int) PRDetail {
	var detail PRDetail
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		detail.PR, detail.PreviewErr = c.FindPreview(number)
	}()
	go func() {
		defer wg.Done()
		detail.Comments, detail.Activities, detail.CommentsErr, detail.ActivitiesErr = c.IssueDetail(number)
	}()
	wg.Wait()
	return detail
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
