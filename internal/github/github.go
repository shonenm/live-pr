// Package github wraps the GitHub CLI operations live-pr needs.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrPRNotFound means no open pull request exists for the requested head.
var ErrPRNotFound = errors.New("pull request not found")

// PR is the remote pull-request state needed by live-pr.
type PR struct {
	Number            int                     `json:"number"`
	URL               string                  `json:"url"`
	Title             string                  `json:"title"`
	Body              string                  `json:"body"`
	State             string                  `json:"state"`
	BaseRefName       string                  `json:"baseRefName,omitempty"`
	HeadRefName       string                  `json:"headRefName,omitempty"`
	HeadRefOID        string                  `json:"headRefOid,omitempty"`
	IsDraft           bool                    `json:"isDraft,omitempty"`
	IsCrossRepository bool                    `json:"isCrossRepository,omitempty"`
	Mergeable         string                  `json:"mergeable,omitempty"`
	MergeStateStatus  string                  `json:"mergeStateStatus,omitempty"`
	ReviewDecision    string                  `json:"reviewDecision,omitempty"`
	Additions         int                     `json:"additions,omitempty"`
	Deletions         int                     `json:"deletions,omitempty"`
	ChangedFiles      int                     `json:"changedFiles,omitempty"`
	UpdatedAt         string                  `json:"updatedAt,omitempty"`
	Conversation      []PRConversationComment `json:"comments,omitempty"`
	CommentCount      int                     `json:"commentCount,omitempty"`
	CommitCount       int                     `json:"commitCount,omitempty"`
	Checks            []PRCheck               `json:"statusCheckRollup,omitempty"`
	Author            PRUser                  `json:"author,omitempty"`
	CreatedAt         string                  `json:"createdAt,omitempty"`
	Assignees         []PRUser                `json:"assignees,omitempty"`
	Labels            []PRLabel               `json:"labels,omitempty"`
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

// Client runs GitHub operations through gh.
type Client struct{ run runner }

// New returns a GitHub CLI client.
func New() Client {
	return Client{run: func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	}}
}

// FindOpen finds the open PR for head. Operational errors are returned as-is;
// only a successful empty list becomes ErrPRNotFound.
const prFields = "number,url,title,body,state,baseRefName,headRefName,headRefOid,isDraft,isCrossRepository,author,createdAt,assignees,labels"

func (c Client) FindOpen(head string) (PR, error) {
	out, err := c.run("pr", "list", "--head", head, "--state", "open", "--limit", "1", "--json", prFields)
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

// ListOpen returns the newest 100 open pull requests with bounded preview
// metadata: one top comment, exact comment/commit totals, and up to 100 checks.
func (c Client) ListOpen() ([]PR, error) {
	out, err := c.run("repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return nil, commandError("gh repo view", out, err)
	}
	var repo struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal(out, &repo); err != nil {
		return nil, fmt.Errorf("decode gh repo view: %w", err)
	}
	owner, name, ok := strings.Cut(repo.NameWithOwner, "/")
	if !ok {
		return nil, fmt.Errorf("invalid repository %q", repo.NameWithOwner)
	}
	const query = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){pullRequests(first:100,states:OPEN,orderBy:{field:CREATED_AT,direction:DESC}){nodes{number url title body state baseRefName headRefName headRefOid isDraft isCrossRepository mergeable mergeStateStatus reviewDecision additions deletions changedFiles updatedAt createdAt author{login} assignees(first:10){nodes{login}} labels(first:20){nodes{name color}} comments(first:1){totalCount nodes{author{login} body createdAt url}} commits{totalCount} statusCheckRollup{contexts(first:100){nodes{... on CheckRun{name status conclusion} ... on StatusContext{context state}}}}}}}}`
	out, err = c.run("api", "graphql", "-F", "owner="+owner, "-F", "name="+name, "-f", "query="+query)
	if err != nil {
		return nil, commandError("gh api graphql", out, err)
	}
	type listNode struct {
		PR
		Assignees struct {
			Nodes []PRUser `json:"nodes"`
		} `json:"assignees"`
		Labels struct {
			Nodes []PRLabel `json:"nodes"`
		} `json:"labels"`
		Comments struct {
			TotalCount int                     `json:"totalCount"`
			Nodes      []PRConversationComment `json:"nodes"`
		} `json:"comments"`
		Commits struct {
			TotalCount int `json:"totalCount"`
		} `json:"commits"`
		StatusCheckRollup struct {
			Contexts struct {
				Nodes []PRCheck `json:"nodes"`
			} `json:"contexts"`
		} `json:"statusCheckRollup"`
	}
	var result struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					Nodes []listNode `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("decode PR list: %w", err)
	}
	prs := make([]PR, 0, len(result.Data.Repository.PullRequests.Nodes))
	for _, node := range result.Data.Repository.PullRequests.Nodes {
		pr := node.PR
		pr.Assignees = node.Assignees.Nodes
		pr.Labels = node.Labels.Nodes
		pr.Conversation = node.Comments.Nodes
		pr.CommentCount = node.Comments.TotalCount
		pr.CommitCount = node.Commits.TotalCount
		pr.Checks = node.StatusCheckRollup.Contexts.Nodes
		prs = append(prs, pr)
	}
	return prs, nil
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
