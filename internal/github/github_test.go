package github

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFindOpen(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return []byte(`[{"number":12,"url":"https://example/pr/12","title":"title","body":"body","state":"OPEN","baseRefName":"release","baseRefOid":"base123","author":{"login":"octocat"},"createdAt":"2026-08-07T14:49:25Z","assignees":[{"login":"alice"}],"labels":[{"name":"bug","color":"d73a4a"}]}]`), nil
	}}
	pr, err := c.FindOpen("feature")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Body != "body" || pr.BaseRefName != "release" || pr.BaseRefOID != "base123" || pr.Author.Login != "octocat" || pr.CreatedAt != "2026-08-07T14:49:25Z" || len(pr.Assignees) != 1 || pr.Assignees[0].Login != "alice" || len(pr.Labels) != 1 || pr.Labels[0].Name != "bug" {
		t.Fatalf("unexpected PR: %#v", pr)
	}
	if args := strings.Join(got, " "); !strings.Contains(args, "author") || !strings.Contains(args, "createdAt") || !strings.Contains(args, "assignees") || !strings.Contains(args, "labels") {
		t.Fatalf("metadata fields not requested: %v", got)
	}
}

func TestFindForHeadFallsBackToNewestClosedPR(t *testing.T) {
	var states []string
	client := Client{run: func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "--state open"):
			states = append(states, "open")
			return []byte(`[]`), nil
		case strings.Contains(joined, "--state all"):
			states = append(states, "all")
			return []byte(`[{"number":12,"state":"MERGED","headRefName":"feature"}]`), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}}
	pr, err := client.FindForHead("feature")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.State != "MERGED" || !reflect.DeepEqual(states, []string{"open", "all"}) {
		t.Fatalf("FindForHead = %#v states=%v", pr, states)
	}
}

func TestFindByNumber(t *testing.T) {
	var got []string
	client := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return []byte(`{"number":12,"headRefName":"fork-branch","isCrossRepository":true}`), nil
	}}
	pr, err := client.Find(12)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || !pr.IsCrossRepository || !reflect.DeepEqual(got[:3], []string{"pr", "view", "12"}) {
		t.Fatalf("Find = %#v args=%v", pr, got)
	}
}

func TestSearchPRsReturnsOnePageAndUsesExplicitCursor(t *testing.T) {
	var apiCalls, repoCalls int
	var requests []string
	client := Client{repo: &repositoryIdentity{}, run: func(args ...string) ([]byte, error) {
		if args[0] == "repo" {
			repoCalls++
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		}
		apiCalls++
		requests = append(requests, strings.Join(args, " "))
		if apiCalls == 1 {
			return []byte(`{"data":{"viewer":{"login":"me"},"search":{"issueCount":30,"nodes":[{"number":1,"state":"OPEN","baseRefOid":"base1","reviewDecision":"APPROVED"}],"pageInfo":{"hasNextPage":true,"startCursor":"S1","endCursor":"C1"}}}}`), nil
		}
		return []byte(`{"data":{"viewer":{"login":"me"},"search":{"issueCount":30,"nodes":[{"number":2,"state":"OPEN"}],"pageInfo":{"hasNextPage":false,"startCursor":"S2","endCursor":"C2"}}}}`), nil
	}}
	first, err := client.SearchPRs("is:open assignee:@me", "")
	if err != nil {
		t.Fatal(err)
	}
	if apiCalls != 1 || first.Repository != "acme/repo" || first.TotalCount != 30 || len(first.PRs) != 1 || first.PRs[0].BaseRefOID != "base1" || first.PRs[0].ReviewDecision != "APPROVED" || !first.PageInfo.HasNextPage || first.PageInfo.EndCursor != "C1" {
		t.Fatalf("first page = %#v calls=%d", first, apiCalls)
	}
	second, err := client.SearchPRs("is:open assignee:@me", first.PageInfo.EndCursor)
	if err != nil {
		t.Fatal(err)
	}
	if apiCalls != 2 || repoCalls != 1 || len(second.PRs) != 1 || second.PRs[0].Number != 2 || second.PageInfo.HasNextPage {
		t.Fatalf("second page = %#v calls=%d", second, apiCalls)
	}
	if strings.Contains(requests[0], "after=") || !strings.Contains(requests[0], "pageSize=25") || !strings.Contains(requests[1], "after=C1") || !strings.Contains(requests[0], "repo:acme/repo is:pr is:open assignee:@me sort:updated-desc") || !strings.Contains(requests[0], "author{login avatarUrl}") || !strings.Contains(requests[0], "nodes{login avatarUrl}") || !strings.Contains(requests[0], "reviewDecision") {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestSearchPRsRejectsIncompleteResponses(t *testing.T) {
	for name, test := range map[string]struct {
		response string
		cursor   string
	}{
		"graphql error":  {response: `{"errors":[{"message":"forbidden"}]}`},
		"missing cursor": {response: `{"data":{"search":{"pageInfo":{"hasNextPage":true}}}}`},
		"stuck cursor":   {response: `{"data":{"search":{"pageInfo":{"hasNextPage":true,"endCursor":"C1"}}}}`, cursor: "C1"},
	} {
		t.Run(name, func(t *testing.T) {
			client := Client{repo: &repositoryIdentity{nameWithOwner: "acme/repo"}, run: func(args ...string) ([]byte, error) {
				return []byte(test.response), nil
			}}
			if _, err := client.SearchPRs("is:open", test.cursor); err == nil {
				t.Fatalf("response %s was accepted", test.response)
			}
		})
	}
}

func TestPRDetailStartsPreviewCommentsAndActivityConcurrently(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	client := Client{repo: &repositoryIdentity{nameWithOwner: "acme/repo"}, run: func(args ...string) ([]byte, error) {
		started <- strings.Join(args, " ")
		<-release
		switch {
		case args[0] == "pr":
			return []byte(`{"number":12,"comments":[],"statusCheckRollup":[]}`), nil
		case len(args) > 1 && args[1] == "graphql":
			return []byte(`{"data":{"repository":{"pullRequest":{"commits":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`), nil
		default:
			return []byte(`[[]]`), nil
		}
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.LoadPRDetail(12, PRDetail{})
	}()
	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("preview/comments/activity did not start concurrently")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PR detail did not finish")
	}
}

func TestFindChecksLoadsStateHeadAndRollup(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		// State rides along: the poll outlives a merge, and its caller needs
		// to know that before pushing this copy back into the list.
		if got := strings.Join(args, " "); got != "pr view 12 --json number,state,headRefOid,statusCheckRollup" {
			t.Fatalf("FindChecks args = %q", got)
		}
		return []byte(`{"number":12,"state":"MERGED","headRefOid":"abc","statusCheckRollup":[{"name":"test","status":"IN_PROGRESS"}]}`), nil
	}}
	pr, err := client.FindChecks(12)
	if err != nil || pr.Number != 12 || pr.State != "MERGED" || pr.HeadRefOID != "abc" || len(pr.Checks) != 1 || !pr.PreviewLoaded {
		t.Fatalf("FindChecks = %#v err=%v", pr, err)
	}
}

func TestFindChecksCarriesCheckLogURLs(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		// A check run reports detailsUrl, a legacy status context targetUrl;
		// both normalize to URL() so the checks tab can open the log page.
		return []byte(`{"number":12,"state":"OPEN","headRefOid":"abc","statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://example/runs/1"},{"context":"ci/legacy","state":"FAILURE","targetUrl":"https://example/status/2"},{"name":"pending","status":"QUEUED"}]}`), nil
	}}
	pr, err := client.FindChecks(12)
	if err != nil || len(pr.Checks) != 3 {
		t.Fatalf("FindChecks = %#v err=%v", pr, err)
	}
	if got := pr.Checks[0].URL(); got != "https://example/runs/1" {
		t.Fatalf("check run URL = %q", got)
	}
	if got := pr.Checks[1].URL(); got != "https://example/status/2" {
		t.Fatalf("status context URL = %q", got)
	}
	if got := pr.Checks[2].URL(); got != "" {
		t.Fatalf("URL-less check URL = %q", got)
	}
}

func TestFindPreviewLoadsExpensiveFieldsAndCommitStatuses(t *testing.T) {
	var previewFields, graphqlQuery string
	client := Client{run: func(args ...string) ([]byte, error) {
		switch args[0] {
		case "pr":
			previewFields = args[len(args)-1]
			return []byte(`{"number":12,"body":"body","reviewDecision":"CHANGES_REQUESTED","comments":[{"author":{"login":"alice"},"body":"review","createdAt":"2026-08-10T00:00:00Z"}],"statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`), nil
		case "repo":
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		default:
			graphqlQuery = args[len(args)-1]
			return []byte(`{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":34,"title":"Crash on empty diff"}]},"commits":{"nodes":[{"commit":{"oid":"aaaa","committedDate":"2026-08-10T00:00:00Z","messageHeadline":"first","statusCheckRollup":{"state":"SUCCESS"}}},{"commit":{"oid":"bbbb","committedDate":"2026-08-10T01:00:00Z","messageHeadline":"second","statusCheckRollup":{"state":"FAILURE"}}}],"pageInfo":{"hasNextPage":false}}}}}}`), nil
		}
	}}
	pr, err := client.FindPreview(12)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Body != "body" || pr.ReviewDecision != "CHANGES_REQUESTED" || len(pr.Conversation) != 1 || pr.CommentCount != 1 || pr.CommitCount != 2 || len(pr.Checks) != 1 || len(pr.Commits) != 2 || pr.Commits[0].CheckRollupState != "SUCCESS" || pr.Commits[1].CheckRollupState != "FAILURE" || !pr.PreviewLoaded {
		t.Fatalf("preview = %#v", pr)
	}
	if len(pr.ClosingIssues) != 1 || pr.ClosingIssues[0].Number != 34 || pr.ClosingIssues[0].Title != "Crash on empty diff" {
		t.Fatalf("closing issues = %#v", pr.ClosingIssues)
	}
	if !strings.Contains(graphqlQuery, "closingIssuesReferences(first:10){nodes{number title}}") {
		t.Fatalf("preview GraphQL does not request closing issues: %q", graphqlQuery)
	}
	if !strings.Contains(previewFields, "baseRefOid") || !strings.Contains(previewFields, "reviewDecision") {
		t.Fatalf("gh pr preview omitted required fields: %q", previewFields)
	}
	if strings.Contains(previewFields, "commits") {
		t.Fatalf("gh pr preview still duplicates GraphQL commits: %q", previewFields)
	}
}

func TestCommitStatusRollupsPaginates(t *testing.T) {
	calls := 0
	client := Client{repo: &repositoryIdentity{nameWithOwner: "acme/repo"}, run: func(args ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(`{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":7,"title":"first page"}]},"commits":{"nodes":[{"commit":{"oid":"aaaa","statusCheckRollup":{"state":"SUCCESS"}}}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`), nil
		}
		if !strings.Contains(strings.Join(args, " "), "after=next") {
			t.Fatalf("second page args = %v", args)
		}
		return []byte(`{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"title":"repeated copy"}]},"commits":{"nodes":[{"commit":{"oid":"bbbb","statusCheckRollup":{"state":"FAILURE"}}}],"pageInfo":{"hasNextPage":false}}}}}}`), nil
	}}
	commits, issues, err := client.commitStatusRollups(12)
	if err != nil || calls != 2 || len(commits) != 2 || commits[1].CheckRollupState != "FAILURE" {
		t.Fatalf("commit statuses = %#v calls=%d err=%v", commits, calls, err)
	}
	if len(issues) != 1 || issues[0].Number != 7 {
		t.Fatalf("closing issues must come from the first page: %#v", issues)
	}
}

func TestCommitStatusRollupsRejectsIncompleteGraphQLResponses(t *testing.T) {
	for name, response := range map[string]string{
		"graphql error":  `{"errors":[{"message":"forbidden"}]}`,
		"missing cursor": `{"data":{"repository":{"pullRequest":{"commits":{"pageInfo":{"hasNextPage":true,"endCursor":""}}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := Client{repo: &repositoryIdentity{nameWithOwner: "acme/repo"}, run: func(args ...string) ([]byte, error) {
				return []byte(response), nil
			}}
			if _, _, err := client.commitStatusRollups(12); err == nil {
				t.Fatalf("commit status response %s was accepted", response)
			}
		})
	}
}

func TestFindOpenDistinguishesMissingAndFailure(t *testing.T) {
	missing := Client{run: func(args ...string) ([]byte, error) { return []byte("[]"), nil }}
	if _, err := missing.FindOpen("feature"); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("expected ErrPRNotFound, got %v", err)
	}

	failed := Client{run: func(args ...string) ([]byte, error) { return []byte("network down"), errors.New("exit 1") }}
	if _, err := failed.FindOpen("feature"); err == nil || errors.Is(err, ErrPRNotFound) || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("expected operational error, got %v", err)
	}
}

func TestPRActionsUseExplicitNonInteractiveCommands(t *testing.T) {
	var got [][]string
	client := Client{run: func(args ...string) ([]byte, error) {
		got = append(got, append([]string(nil), args...))
		return nil, nil
	}}
	if err := client.Merge(12, "abc123", MergeCommit); err != nil {
		t.Fatal(err)
	}
	if err := client.Merge(12, "abc123", MergeSquash); err != nil {
		t.Fatal(err)
	}
	if err := client.Merge(12, "abc123", MergeRebase); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkout(34); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(56); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"pr", "merge", "12", "--merge", "--match-head-commit", "abc123"},
		{"pr", "merge", "12", "--squash", "--match-head-commit", "abc123"},
		{"pr", "merge", "12", "--rebase", "--match-head-commit", "abc123"},
		{"pr", "checkout", "34"},
		{"pr", "close", "56"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
	if err := client.Merge(12, "abc123", MergeMethod("bogus")); err == nil || !strings.Contains(err.Error(), "unknown merge method") {
		t.Fatalf("bogus-method Merge error = %v", err)
	}
}

func TestSetStatusUsesGitHubTransitions(t *testing.T) {
	var calls [][]string
	client := Client{run: func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	}}
	if err := client.SetStatus(PR{Number: 12, State: "CLOSED"}, "draft"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetStatus(PR{Number: 13, State: "OPEN", IsDraft: true}, "open"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"pr", "reopen", "12"}, {"pr", "ready", "12", "--undo"}, {"pr", "ready", "13"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("status calls = %#v, want %#v", calls, want)
	}
	if err := client.SetStatus(PR{Number: 14, State: "MERGED"}, "open"); err == nil {
		t.Fatal("merged PR status change accepted")
	}
}

func TestPRActionsReturnCommandOutput(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		return []byte("merge blocked"), errors.New("exit 1")
	}}
	if err := client.Merge(12, "abc123", MergeCommit); err == nil || !strings.Contains(err.Error(), "merge blocked") {
		t.Fatalf("Merge error = %v", err)
	}
	if err := client.Checkout(12); err == nil || !strings.Contains(err.Error(), "merge blocked") {
		t.Fatalf("Checkout error = %v", err)
	}
	if err := client.Close(12); err == nil || !strings.Contains(err.Error(), "merge blocked") {
		t.Fatalf("Close error = %v", err)
	}
	if err := client.Merge(12, "", MergeCommit); err == nil || !strings.Contains(err.Error(), "reviewed head") {
		t.Fatalf("empty-head Merge error = %v", err)
	}
}

func TestIssueCommentsFlattensPages(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return []byte(`[[{"id":1,"body":"first","user":{"login":"alice"}}],[{"id":2,"body":"second","user":{"login":"bob"}}]]`), nil
	}}
	comments, err := c.IssueComments(12)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 || comments[1].User.Login != "bob" {
		t.Fatalf("unexpected comments: %#v", comments)
	}
	want := []string{"api", "--paginate", "--slurp", "repos/{owner}/{repo}/issues/12/comments?per_page=100"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}
}

// detailRun serves every request LoadPRDetail issues. The preview reports
// commentCount conversation entries, and the issue-comments endpoint answers
// from responses in call order while recording the full argv of each call.
type detailRun struct {
	mu           sync.Mutex
	commentCount int
	responses    []commentsResponse
	calls        []string
}

type commentsResponse struct {
	out string
	err error
}

func (f *detailRun) run(args ...string) ([]byte, error) {
	endpoint := args[len(args)-1]
	switch {
	case args[0] == "pr":
		entries := strings.TrimSuffix(strings.Repeat("{},", f.commentCount), ",")
		return []byte(`{"number":12,"comments":[` + entries + `],"statusCheckRollup":[]}`), nil
	case args[1] == "graphql":
		return []byte(`{"data":{"repository":{"pullRequest":{"commits":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`), nil
	case strings.Contains(endpoint, "/issues/12/comments"):
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, strings.Join(args, " "))
		if len(f.responses) == 0 {
			return []byte(`[[]]`), nil
		}
		next := f.responses[0]
		f.responses = f.responses[1:]
		return []byte(next.out), next.err
	default:
		return []byte(`[[]]`), nil
	}
}

func detailClient(f *detailRun) Client {
	return Client{repo: &repositoryIdentity{nameWithOwner: "acme/repo"}, run: f.run}
}

func TestLoadPRDetailFetchesCommentsIncrementally(t *testing.T) {
	f := &detailRun{commentCount: 2, responses: []commentsResponse{
		{out: `[[{"id":1,"body":"edited","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T13:00:00Z","user":{"login":"alice"}}]]`},
	}}
	prev := PRDetail{PR: PR{Number: 12}, Comments: []Comment{
		{ID: 1, Body: "old", CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z"},
		{ID: 2, Body: "kept", CreatedAt: "2026-08-01T11:00:00Z", UpdatedAt: "2026-08-01T12:00:00Z"},
	}}
	detail := detailClient(f).LoadPRDetail(12, prev)
	if detail.CommentsErr != nil {
		t.Fatal(detail.CommentsErr)
	}
	want := "api --paginate --slurp repos/{owner}/{repo}/issues/12/comments?per_page=100&since=2026-08-01T12%3A00%3A00Z"
	if len(f.calls) != 1 || f.calls[0] != want {
		t.Fatalf("comment calls = %q, want [%q]", f.calls, want)
	}
	if len(detail.Comments) != 2 || detail.Comments[0].ID != 1 || detail.Comments[0].Body != "edited" || detail.Comments[1].Body != "kept" {
		t.Fatalf("merged comments = %#v", detail.Comments)
	}
}

func TestLoadPRDetailAppendsNewCommentInOrder(t *testing.T) {
	f := &detailRun{commentCount: 2, responses: []commentsResponse{
		{out: `[[{"id":3,"body":"newest","created_at":"2026-08-01T12:00:00Z","updated_at":"2026-08-01T12:00:00Z"}]]`},
	}}
	prev := PRDetail{PR: PR{Number: 12}, Comments: []Comment{
		{ID: 1, Body: "first", CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z"},
	}}
	detail := detailClient(f).LoadPRDetail(12, prev)
	if detail.CommentsErr != nil {
		t.Fatal(detail.CommentsErr)
	}
	if len(detail.Comments) != 2 || detail.Comments[0].ID != 1 || detail.Comments[1].ID != 3 {
		t.Fatalf("merged comments = %#v", detail.Comments)
	}
}

func TestLoadPRDetailCountMismatchTriggersFullRefetch(t *testing.T) {
	// The preview counts one comment while the incremental merge keeps two:
	// comment 2 was deleted remotely, which since= can never report.
	f := &detailRun{commentCount: 1, responses: []commentsResponse{
		{out: `[[]]`},
		{out: `[[{"id":1,"body":"kept","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}]]`},
	}}
	prev := PRDetail{PR: PR{Number: 12}, Comments: []Comment{
		{ID: 1, Body: "kept", CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z"},
		{ID: 2, Body: "deleted remotely", CreatedAt: "2026-08-01T11:00:00Z", UpdatedAt: "2026-08-01T11:00:00Z"},
	}}
	detail := detailClient(f).LoadPRDetail(12, prev)
	if detail.CommentsErr != nil {
		t.Fatal(detail.CommentsErr)
	}
	if len(f.calls) != 2 || !strings.Contains(f.calls[0], "since=") || strings.Contains(f.calls[1], "since=") {
		t.Fatalf("comment calls = %q, want an incremental fetch then a full refetch", f.calls)
	}
	if len(detail.Comments) != 1 || detail.Comments[0].ID != 1 {
		t.Fatalf("refetched comments = %#v", detail.Comments)
	}
}

func TestLoadPRDetailEmptyPrevDoesFullFetch(t *testing.T) {
	f := &detailRun{commentCount: 0}
	detail := detailClient(f).LoadPRDetail(12, PRDetail{})
	if detail.CommentsErr != nil {
		t.Fatal(detail.CommentsErr)
	}
	want := "api --paginate --slurp repos/{owner}/{repo}/issues/12/comments?per_page=100"
	if len(f.calls) != 1 || f.calls[0] != want {
		t.Fatalf("comment calls = %q, want [%q]", f.calls, want)
	}
}

func TestLoadPRDetailIncrementalErrorFallsBackToFullFetch(t *testing.T) {
	f := &detailRun{commentCount: 2, responses: []commentsResponse{
		{out: `rate limited`, err: errors.New("exit 1")},
		{out: `[[{"id":1,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"},{"id":2,"created_at":"2026-08-01T11:00:00Z","updated_at":"2026-08-01T11:00:00Z"}]]`},
	}}
	prev := PRDetail{PR: PR{Number: 12}, Comments: []Comment{
		{ID: 1, CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z"},
	}}
	detail := detailClient(f).LoadPRDetail(12, prev)
	if detail.CommentsErr != nil {
		t.Fatalf("a failed incremental fetch must fall back silently, got %v", detail.CommentsErr)
	}
	if len(f.calls) != 2 || !strings.Contains(f.calls[0], "since=") || strings.Contains(f.calls[1], "since=") {
		t.Fatalf("comment calls = %q, want an incremental attempt then a full fetch", f.calls)
	}
	if len(detail.Comments) != 2 {
		t.Fatalf("comments = %#v", detail.Comments)
	}
}

func TestIssueActivitiesFlattensPages(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return []byte(`[[{"id":1,"event":"labeled","actor":{"login":"alice"},"label":{"name":"bug"}}],[{"id":2,"event":"closed"}]]`), nil
	}}
	activities, err := c.IssueActivities(12)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 || activities[0].Actor.Login != "alice" || activities[0].Label.Name != "bug" {
		t.Fatalf("unexpected activities: %#v", activities)
	}
	want := []string{"api", "--paginate", "--slurp", "repos/{owner}/{repo}/issues/12/events?per_page=100"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}
}

func TestUpdateArgumentsAndError(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return nil, nil
	}}
	if err := c.Update(41, "title", "/tmp/body"); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr", "edit", "41", "--title", "title", "--body-file", "/tmp/body"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}
	if err := c.UpdateBody(41, "/tmp/body"); err != nil {
		t.Fatal(err)
	}
	want = []string{"pr", "edit", "41", "--body-file", "/tmp/body"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}

	failed := Client{run: func(args ...string) ([]byte, error) { return []byte("denied"), errors.New("exit 1") }}
	if err := failed.Update(41, "title", "/tmp/body"); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected contextual update error, got %v", err)
	}
}

func TestCreateArguments(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return []byte("https://example/pr/1\n"), nil
	}}
	url, err := c.Create("main", "feature", "title", "/tmp/body", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pr", "create", "--base", "main", "--head", "feature", "--title", "title", "--body-file", "/tmp/body", "--draft"}
	if !reflect.DeepEqual(got, want) || url != "https://example/pr/1" {
		t.Fatalf("args=%v url=%q", got, url)
	}
}

func TestSlowOperationsRunWithLongTimeout(t *testing.T) {
	var gotTimeout time.Duration
	var gotArgs []string
	c := Client{runTimeout: func(timeout time.Duration, args ...string) ([]byte, error) {
		gotTimeout, gotArgs = timeout, append([]string(nil), args...)
		return []byte("[]"), nil
	}}
	if _, err := c.IssueComments(12); err != nil {
		t.Fatal(err)
	}
	if gotTimeout != longRunTimeout {
		t.Fatalf("paginated list timeout=%s, want %s", gotTimeout, longRunTimeout)
	}
	if want := []string{"api", "--paginate", "--slurp", "repos/{owner}/{repo}/issues/12/comments?per_page=100"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args=%v", gotArgs)
	}
	if err := c.Checkout(7); err != nil {
		t.Fatal(err)
	}
	if gotTimeout != longRunTimeout {
		t.Fatalf("checkout timeout=%s, want %s", gotTimeout, longRunTimeout)
	}
	if want := []string{"pr", "checkout", "7"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args=%v", gotArgs)
	}
}

func TestRunWithTimeoutFallsBackToDefaultRunner(t *testing.T) {
	ran := false
	c := Client{run: func(args ...string) ([]byte, error) {
		ran = true
		return []byte("[]"), nil
	}}
	if _, err := c.IssueComments(12); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("without runTimeout the default runner must handle the call")
	}
}

func TestRunErrorFoldsTimeoutAndStderr(t *testing.T) {
	if runError(nil, defaultRunTimeout, false, "warning: ignored") != nil {
		t.Fatal("nil error must stay nil")
	}
	base := errors.New("signal: killed")
	got := runError(base, defaultRunTimeout, true, "  gh: request timed out\n")
	if !errors.Is(got, base) {
		t.Fatal("wrapped error must keep the cause")
	}
	want := "timed out after 30s: signal: killed: gh: request timed out"
	if got.Error() != want {
		t.Fatalf("got %q, want %q", got.Error(), want)
	}
	if msg := runError(base, defaultRunTimeout, false, "").Error(); msg != "signal: killed" {
		t.Fatalf("no timeout, no stderr: got %q", msg)
	}
}

func TestFindPreviewSurvivesCommitRollupFailure(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		switch args[0] {
		case "pr":
			return []byte(`{"number":12,"body":"body","comments":[],"statusCheckRollup":[]}`), nil
		case "repo":
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		default:
			return nil, errors.New("graphql down")
		}
	}}
	pr, err := client.FindPreview(12)
	if err != nil {
		t.Fatalf("rollup failure discarded the preview: %v", err)
	}
	if pr.Number != 12 || !pr.PreviewLoaded || len(pr.Commits) != 0 {
		t.Fatalf("partial preview = %#v", pr)
	}
}

func TestStatusHint(t *testing.T) {
	longStderr := "GraphQL: " + strings.Repeat("x", 100)
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"gh not installed", commandError("gh pr list", nil, runError(&exec.Error{Name: "gh", Err: exec.ErrNotFound}, defaultRunTimeout, false, "")), "gh not installed"},
		{"unauthenticated stderr", commandError("gh pr list", nil, runError(errors.New("exit status 4"), defaultRunTimeout, false, "To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable.")), "run gh auth login"},
		{"expired token", commandError("gh api graphql", nil, runError(errors.New("exit status 1"), defaultRunTimeout, false, "HTTP 401: Bad credentials (https://api.github.com/graphql)")), "run gh auth login"},
		{"broken query stderr", commandError("gh api graphql", nil, runError(errors.New("exit status 1"), defaultRunTimeout, false, "GraphQL: Field 'nope' doesn't exist on type 'PullRequest' (search)")), "GraphQL: Field 'nope' doesn't exist on type 'PullRequest' (search)"},
		{"long stderr truncated", commandError("gh api graphql", nil, runError(errors.New("exit status 1"), defaultRunTimeout, false, longStderr)), string([]rune(longStderr)[:79]) + "…"},
		{"offline gh stderr", commandError("gh pr list", nil, runError(errors.New("exit status 1"), defaultRunTimeout, false, "error connecting to api.github.com\ncheck your internet connection")), ""},
		{"plain network error", errors.New("dial tcp 140.82.113.6:443: connect: connection refused"), ""},
		{"timeout", commandError("gh pr list", nil, runError(errors.New("signal: killed"), defaultRunTimeout, true, "")), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusHint(tc.err); got != tc.want {
				t.Fatalf("StatusHint(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestCommentWriteCommandsTargetExplicitEndpoints(t *testing.T) {
	for name, test := range map[string]struct {
		call     func(Client) error
		method   string
		endpoint string
		body     string
	}{
		"post": {
			call:     func(c Client) error { return c.PostIssueComment(7, "hello\n\"world\"") },
			method:   "POST",
			endpoint: "repos/{owner}/{repo}/issues/7/comments",
			body:     "hello\n\"world\"",
		},
		"edit": {
			call:     func(c Client) error { return c.EditIssueComment(9001, "updated") },
			method:   "PATCH",
			endpoint: "repos/{owner}/{repo}/issues/comments/9001",
			body:     "updated",
		},
		"delete": {
			call:     func(c Client) error { return c.DeleteIssueComment(9001) },
			method:   "DELETE",
			endpoint: "repos/{owner}/{repo}/issues/comments/9001",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var got []string
			var payload []byte
			client := Client{run: func(args ...string) ([]byte, error) {
				got = append([]string(nil), args...)
				// The --input temp file is deleted after run returns, so the
				// payload must be captured while the command is "executing".
				for i, arg := range args[:len(args)-1] {
					if arg == "--input" {
						payload, _ = os.ReadFile(args[i+1])
					}
				}
				return nil, nil
			}}
			if err := test.call(client); err != nil {
				t.Fatal(err)
			}
			if len(got) < 4 || got[0] != "api" || got[1] != "--method" || got[2] != test.method || got[3] != test.endpoint {
				t.Fatalf("args = %v, want api --method %s %s", got, test.method, test.endpoint)
			}
			if test.body == "" {
				if len(got) != 4 {
					t.Fatalf("delete sent extra arguments: %v", got)
				}
				return
			}
			var sent struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal(payload, &sent); err != nil || sent.Body != test.body {
				t.Fatalf("payload = %q (%v), want body %q", payload, err, test.body)
			}
		})
	}
}

func TestCommentWritesRejectEmptyBodies(t *testing.T) {
	for name, call := range map[string]func(Client) error{
		"post blank":      func(c Client) error { return c.PostIssueComment(7, "") },
		"post whitespace": func(c Client) error { return c.PostIssueComment(7, " \n\t") },
		"edit blank":      func(c Client) error { return c.EditIssueComment(9001, "") },
	} {
		t.Run(name, func(t *testing.T) {
			ran := false
			client := Client{run: func(args ...string) ([]byte, error) {
				ran = true
				return nil, nil
			}}
			err := call(client)
			if err == nil || !strings.Contains(err.Error(), "comment body must not be empty") {
				t.Fatalf("error = %v, want empty-body rejection", err)
			}
			if ran {
				t.Fatal("gh ran for an empty body")
			}
		})
	}
}

func TestCommentWriteErrorsCarryCommandOutput(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		return []byte("gh: rate limited"), errors.New("exit status 1")
	}}
	for name, test := range map[string]struct {
		call  func() error
		label string
	}{
		"post":   {call: func() error { return client.PostIssueComment(7, "hi") }, label: "gh api post issue comment"},
		"edit":   {call: func() error { return client.EditIssueComment(9001, "hi") }, label: "gh api edit issue comment"},
		"delete": {call: func() error { return client.DeleteIssueComment(9001) }, label: "gh api delete issue comment"},
	} {
		t.Run(name, func(t *testing.T) {
			err := test.call()
			if err == nil || !strings.Contains(err.Error(), test.label) || !strings.Contains(err.Error(), "rate limited") {
				t.Fatalf("error = %v, want %q wrapping the command output", err, test.label)
			}
		})
	}
}
