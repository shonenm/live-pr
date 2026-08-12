package github

import (
	"errors"
	"reflect"
	"strings"
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
			return []byte(`{"data":{"viewer":{"login":"me"},"search":{"issueCount":30,"nodes":[{"number":1,"state":"OPEN","baseRefOid":"base1"}],"pageInfo":{"hasNextPage":true,"startCursor":"S1","endCursor":"C1"}}}}`), nil
		}
		return []byte(`{"data":{"viewer":{"login":"me"},"search":{"issueCount":30,"nodes":[{"number":2,"state":"OPEN"}],"pageInfo":{"hasNextPage":false,"startCursor":"S2","endCursor":"C2"}}}}`), nil
	}}
	first, err := client.SearchPRs("is:open assignee:@me", "")
	if err != nil {
		t.Fatal(err)
	}
	if apiCalls != 1 || first.Repository != "acme/repo" || first.TotalCount != 30 || len(first.PRs) != 1 || first.PRs[0].BaseRefOID != "base1" || !first.PageInfo.HasNextPage || first.PageInfo.EndCursor != "C1" {
		t.Fatalf("first page = %#v calls=%d", first, apiCalls)
	}
	second, err := client.SearchPRs("is:open assignee:@me", first.PageInfo.EndCursor)
	if err != nil {
		t.Fatal(err)
	}
	if apiCalls != 2 || repoCalls != 1 || len(second.PRs) != 1 || second.PRs[0].Number != 2 || second.PageInfo.HasNextPage {
		t.Fatalf("second page = %#v calls=%d", second, apiCalls)
	}
	if strings.Contains(requests[0], "after=") || !strings.Contains(requests[0], "pageSize=25") || !strings.Contains(requests[1], "after=C1") || !strings.Contains(requests[0], "repo:acme/repo is:pr is:open assignee:@me sort:updated-desc") {
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

func TestIssueDetailStartsIndependentRequestsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	client := Client{run: func(args ...string) ([]byte, error) {
		started <- struct{}{}
		<-release
		return []byte(`[[]]`), nil
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, commentsErr, activitiesErr := client.IssueDetail(12)
		if commentsErr != nil || activitiesErr != nil {
			t.Errorf("IssueDetail errors = %v, %v", commentsErr, activitiesErr)
		}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("comment and activity requests did not overlap")
		}
	}
	close(release)
	<-done
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
		_ = client.LoadPRDetail(12)
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

func TestFindChecksLoadsOnlyHeadAndRollup(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		if got := strings.Join(args, " "); got != "pr view 12 --json number,headRefOid,statusCheckRollup" {
			t.Fatalf("FindChecks args = %q", got)
		}
		return []byte(`{"number":12,"headRefOid":"abc","statusCheckRollup":[{"name":"test","status":"IN_PROGRESS"}]}`), nil
	}}
	pr, err := client.FindChecks(12)
	if err != nil || pr.Number != 12 || pr.HeadRefOID != "abc" || len(pr.Checks) != 1 || !pr.PreviewLoaded {
		t.Fatalf("FindChecks = %#v err=%v", pr, err)
	}
}

func TestFindPreviewLoadsExpensiveFieldsAndCommitStatuses(t *testing.T) {
	var previewFields string
	client := Client{run: func(args ...string) ([]byte, error) {
		switch args[0] {
		case "pr":
			previewFields = args[len(args)-1]
			return []byte(`{"number":12,"body":"body","comments":[{"author":{"login":"alice"},"body":"review","createdAt":"2026-08-10T00:00:00Z"}],"statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`), nil
		case "repo":
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		default:
			return []byte(`{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"aaaa","committedDate":"2026-08-10T00:00:00Z","messageHeadline":"first","statusCheckRollup":{"state":"SUCCESS"}}},{"commit":{"oid":"bbbb","committedDate":"2026-08-10T01:00:00Z","messageHeadline":"second","statusCheckRollup":{"state":"FAILURE"}}}],"pageInfo":{"hasNextPage":false}}}}}}`), nil
		}
	}}
	pr, err := client.FindPreview(12)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Body != "body" || len(pr.Conversation) != 1 || pr.CommentCount != 1 || pr.CommitCount != 2 || len(pr.Checks) != 1 || len(pr.Commits) != 2 || pr.Commits[0].CheckRollupState != "SUCCESS" || pr.Commits[1].CheckRollupState != "FAILURE" || !pr.PreviewLoaded {
		t.Fatalf("preview = %#v", pr)
	}
	if !strings.Contains(previewFields, "baseRefOid") {
		t.Fatalf("gh pr preview omitted historical base OID: %q", previewFields)
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
			return []byte(`{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"aaaa","statusCheckRollup":{"state":"SUCCESS"}}}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`), nil
		}
		if !strings.Contains(strings.Join(args, " "), "after=next") {
			t.Fatalf("second page args = %v", args)
		}
		return []byte(`{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"bbbb","statusCheckRollup":{"state":"FAILURE"}}}],"pageInfo":{"hasNextPage":false}}}}}}`), nil
	}}
	commits, err := client.commitStatusRollups(12)
	if err != nil || calls != 2 || len(commits) != 2 || commits[1].CheckRollupState != "FAILURE" {
		t.Fatalf("commit statuses = %#v calls=%d err=%v", commits, calls, err)
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
			if _, err := client.commitStatusRollups(12); err == nil {
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
	if err := client.Merge(12, "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkout(34); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(56); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"pr", "merge", "12", "--merge", "--match-head-commit", "abc123"}, {"pr", "checkout", "34"}, {"pr", "close", "56"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
}

func TestPRActionsReturnCommandOutput(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		return []byte("merge blocked"), errors.New("exit 1")
	}}
	if err := client.Merge(12, "abc123"); err == nil || !strings.Contains(err.Error(), "merge blocked") {
		t.Fatalf("Merge error = %v", err)
	}
	if err := client.Checkout(12); err == nil || !strings.Contains(err.Error(), "merge blocked") {
		t.Fatalf("Checkout error = %v", err)
	}
	if err := client.Close(12); err == nil || !strings.Contains(err.Error(), "merge blocked") {
		t.Fatalf("Close error = %v", err)
	}
	if err := client.Merge(12, ""); err == nil || !strings.Contains(err.Error(), "reviewed head") {
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
	if err := c.Update("feature", "title", "/tmp/body"); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr", "edit", "feature", "--title", "title", "--body-file", "/tmp/body"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}

	failed := Client{run: func(args ...string) ([]byte, error) { return []byte("denied"), errors.New("exit 1") }}
	if err := failed.Update("feature", "title", "/tmp/body"); err == nil || !strings.Contains(err.Error(), "denied") {
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
