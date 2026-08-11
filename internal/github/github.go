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

// OpenPRs is one repository PR-list snapshot and its authenticated viewer.
type OpenPRs struct {
	ViewerLogin string `json:"viewerLogin,omitempty"`
	PRs         []PR   `json:"prs,omitempty"`
}

// PRConversationComment is compact list-preview conversation metadata.
type PRConversationComment struct {
	Author    PRUser `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	URL       string `json:"url,omitempty"`
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

const prListPageSize = 25

type prListPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type prListNode struct {
	Number            int    `json:"number"`
	URL               string `json:"url"`
	Title             string `json:"title"`
	State             string `json:"state"`
	BaseRefName       string `json:"baseRefName"`
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

type prListPage struct {
	Data struct {
		Viewer          PRUser `json:"viewer"`
		ReviewRequested struct {
			Nodes []struct {
				Number int `json:"number"`
			} `json:"nodes"`
			PageInfo prListPageInfo `json:"pageInfo"`
		} `json:"reviewRequested"`
		Repository struct {
			PullRequests struct {
				Nodes    []prListNode   `json:"nodes"`
				PageInfo prListPageInfo `json:"pageInfo"`
			} `json:"pullRequests"`
		} `json:"repository"`
	} `json:"data"`
}

func (node prListNode) pullRequest(viewerReviewRequested bool) PR {
	pr := PR{
		Number: node.Number, URL: node.URL, Title: node.Title, State: node.State,
		BaseRefName: node.BaseRefName, HeadRefName: node.HeadRefName, HeadRefOID: node.HeadRefOID,
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

func (c Client) requestPRListPage(owner, name, state, reviewQuery, after, reviewAfter, query string) (prListPage, error) {
	args := []string{"api", "graphql", "-F", "owner=" + owner, "-F", "name=" + name, "-F", "state=" + state, "-F", fmt.Sprintf("pageSize=%d", prListPageSize)}
	if state == "OPEN" {
		args = append(args, "-F", "reviewQuery="+reviewQuery)
	}
	if after != "" {
		args = append(args, "-F", "after="+after)
	}
	if reviewAfter != "" {
		args = append(args, "-F", "reviewAfter="+reviewAfter)
	}
	args = append(args, "-f", "query="+query)
	out, err := c.run(args...)
	if err != nil {
		return prListPage{}, commandError("gh api graphql", out, err)
	}
	var page prListPage
	if err := json.Unmarshal(out, &page); err != nil {
		return prListPage{}, fmt.Errorf("decode PR list: %w", err)
	}
	return page, nil
}

// FindOpen finds the open PR for head. Operational errors are returned as-is;
// only a successful empty list becomes ErrPRNotFound.
const prFields = "number,url,title,body,state,baseRefName,headRefName,headRefOid,isDraft,isCrossRepository,author,createdAt,assignees,labels"

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

// ListOpen returns all open pull requests in pages with lightweight row metadata.
func (c Client) ListOpen() (OpenPRs, error) {
	return c.ListState("OPEN")
}

// ListState returns pull requests in one GitHub state with lightweight row metadata.
func (c Client) ListState(state string) (OpenPRs, error) {
	state = strings.ToUpper(strings.TrimSpace(state))
	if state != "OPEN" && state != "CLOSED" {
		return OpenPRs{}, fmt.Errorf("unsupported pull request state %q", state)
	}
	repoName, err := c.repositoryName()
	if err != nil {
		return OpenPRs{}, err
	}
	owner, name, ok := strings.Cut(repoName, "/")
	if !ok {
		return OpenPRs{}, fmt.Errorf("invalid repository %q", repoName)
	}
	// List rows only need metadata. Body, comments, commits, and checks are
	// fetched lazily for the selected PR; loading them for every PR makes large
	// repositories spend most of startup time in GitHub's GraphQL resolver.
	query := `query($owner:String!,$name:String!,$reviewQuery:String!,$state:PullRequestState!,$pageSize:Int!,$after:String,$reviewAfter:String){viewer{login} reviewRequested:search(query:$reviewQuery,type:ISSUE,first:$pageSize,after:$reviewAfter){nodes{... on PullRequest{number}} pageInfo{hasNextPage endCursor}} repository(owner:$owner,name:$name){pullRequests(first:$pageSize,after:$after,states:[$state],orderBy:{field:CREATED_AT,direction:DESC}){nodes{number url title state baseRefName headRefName headRefOid isDraft isCrossRepository mergeable mergeStateStatus reviewDecision updatedAt createdAt author{login} assignees(first:10){nodes{login}} reviewRequests(first:20){nodes{requestedReviewer{... on User{login}}}} labels(first:20){nodes{name color}} statusCheckRollup{state}} pageInfo{hasNextPage endCursor}}}}`
	if state == "CLOSED" {
		query = strings.Replace(query, "states:[$state]", "states:[$state,MERGED]", 1)
		query = strings.Replace(query, "$reviewQuery:String!,", "", 1)
		query = strings.Replace(query, ",$reviewAfter:String", "", 1)
		query = strings.Replace(query, `reviewRequested:search(query:$reviewQuery,type:ISSUE,first:$pageSize,after:$reviewAfter){nodes{... on PullRequest{number}} pageInfo{hasNextPage endCursor}} `, "", 1)
	}
	reviewQuery := "repo:" + repoName + " is:pr is:open review-requested:@me"
	after, reviewAfter := "", ""
	var allNodes []prListNode
	seenPRs := map[int]bool{}
	requested := map[int]bool{}
	viewerLogin := ""
	for {
		page, err := c.requestPRListPage(owner, name, state, reviewQuery, after, reviewAfter, query)
		if err != nil {
			return OpenPRs{}, err
		}
		viewerLogin = page.Data.Viewer.Login
		for _, node := range page.Data.Repository.PullRequests.Nodes {
			if !seenPRs[node.Number] {
				seenPRs[node.Number] = true
				allNodes = append(allNodes, node)
			}
		}
		for _, node := range page.Data.ReviewRequested.Nodes {
			requested[node.Number] = true
		}
		if page.Data.Repository.PullRequests.PageInfo.HasNextPage {
			after = page.Data.Repository.PullRequests.PageInfo.EndCursor
		} else {
			after = ""
		}
		if page.Data.ReviewRequested.PageInfo.HasNextPage {
			reviewAfter = page.Data.ReviewRequested.PageInfo.EndCursor
		} else {
			reviewAfter = ""
		}
		if after == "" && reviewAfter == "" {
			break
		}
	}
	prs := make([]PR, 0, len(allNodes))
	for _, node := range allNodes {
		prs = append(prs, node.pullRequest(requested[node.Number]))
	}
	return OpenPRs{ViewerLogin: viewerLogin, PRs: prs}, nil
}

// FindPreview loads the expensive preview fields for one PR only.
func (c Client) FindPreview(number int) (PR, error) {
	const fields = "number,url,title,body,state,baseRefName,headRefName,headRefOid,isDraft,isCrossRepository,mergeable,mergeStateStatus,reviewDecision,additions,deletions,changedFiles,updatedAt,createdAt,author,assignees,labels,reviewRequests,comments,commits,statusCheckRollup"
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json", fields)
	if err != nil {
		return PR{}, commandError("gh pr view", out, err)
	}
	var preview struct {
		PR
		Conversation []PRConversationComment `json:"comments"`
		Commits      []json.RawMessage       `json:"commits"`
		Checks       []PRCheck               `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &preview); err != nil {
		return PR{}, fmt.Errorf("decode gh pr preview: %w", err)
	}
	preview.PR.Conversation = preview.Conversation
	preview.PR.CommentCount = len(preview.Conversation)
	preview.PR.CommitCount = len(preview.Commits)
	preview.PR.Checks = preview.Checks
	preview.PR.PreviewLoaded = true
	return preview.PR, nil
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
