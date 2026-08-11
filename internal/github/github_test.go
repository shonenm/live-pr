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
		return []byte(`[{"number":12,"url":"https://example/pr/12","title":"title","body":"body","state":"OPEN","baseRefName":"release","author":{"login":"octocat"},"createdAt":"2026-08-07T14:49:25Z","assignees":[{"login":"alice"}],"labels":[{"name":"bug","color":"d73a4a"}]}]`), nil
	}}
	pr, err := c.FindOpen("feature")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Body != "body" || pr.BaseRefName != "release" || pr.Author.Login != "octocat" || pr.CreatedAt != "2026-08-07T14:49:25Z" || len(pr.Assignees) != 1 || pr.Assignees[0].Login != "alice" || len(pr.Labels) != 1 || pr.Labels[0].Name != "bug" {
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

func TestListOpen(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append(got, strings.Join(args, " "))
		switch args[0] {
		case "repo":
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		case "api":
			return []byte(`{"data":{"viewer":{"login":"octocat"},"reviewRequested":{"nodes":[{"number":12}]},"repository":{"pullRequests":{"nodes":[{"number":12,"headRefName":"feature/x","headRefOid":"abc123","baseRefName":"main","isDraft":true,"isCrossRepository":true,"mergeable":"CONFLICTING","mergeStateStatus":"DIRTY","reviewDecision":"CHANGES_REQUESTED","assignees":{"nodes":[{"login":"bob"}]},"reviewRequests":{"nodes":[{"requestedReviewer":{"login":"octocat"}},{"requestedReviewer":{}}]},"labels":{"nodes":[{"name":"bug","color":"d73a4a"}]},"comments":{"totalCount":9,"nodes":[{"author":{"login":"alice"},"body":"review"}]},"commits":{"totalCount":5},"statusCheckRollup":{"contexts":{"nodes":[{"name":"test","status":"COMPLETED","conclusion":"FAILURE"}]}}}]}}}}`), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}}
	list, err := c.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	prs := list.PRs
	if list.ViewerLogin != "octocat" || len(prs) != 1 || prs[0].HeadRefName != "feature/x" || prs[0].HeadRefOID != "abc123" || !prs[0].IsDraft || !prs[0].IsCrossRepository || prs[0].Mergeable != "CONFLICTING" || len(prs[0].Conversation) != 0 || prs[0].CommentCount != 0 || prs[0].CommitCount != 0 || len(prs[0].Checks) != 0 || len(prs[0].Assignees) != 1 || len(prs[0].Labels) != 1 || len(prs[0].ReviewRequests) != 1 || prs[0].ReviewRequests[0].Login != "octocat" || !prs[0].ViewerReviewRequested || prs[0].PreviewLoaded {
		t.Fatalf("open PRs = %#v", list)
	}
	args := strings.Join(got, " ")
	for _, field := range []string{"pullRequests(first:$pageSize", "states:[$state]", "state=OPEN", "pageSize=25", "headRefName", "headRefOid", "isDraft", "isCrossRepository", "mergeable", "mergeStateStatus", "viewer{login}", "reviewRequested:search", "review-requested:@me", "reviewRequests(first:20)", "statusCheckRollup{state}"} {
		if !strings.Contains(args, field) {
			t.Fatalf("list args missing %q: %s", field, args)
		}
	}
	for _, field := range []string{"body", "additions", "deletions", "changedFiles", "comments(first:1)", "statusCheckRollup{contexts", "commits{totalCount}"} {
		if strings.Contains(args, field) {
			t.Fatalf("list args still request expensive field %q: %s", field, args)
		}
	}
}

func TestListClosedIncludesMergedPullRequests(t *testing.T) {
	var query string
	client := Client{run: func(args ...string) ([]byte, error) {
		if args[0] == "repo" {
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		}
		query = strings.Join(args, " ")
		return []byte(`{"data":{"viewer":{"login":"octocat"},"reviewRequested":{"nodes":[],"pageInfo":{"hasNextPage":false}},"repository":{"pullRequests":{"nodes":[{"number":12,"state":"MERGED"}],"pageInfo":{"hasNextPage":false}}}}}`), nil
	}}
	list, err := client.ListState("CLOSED")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.PRs) != 1 || list.PRs[0].State != "MERGED" || !strings.Contains(query, "states:[$state,MERGED]") {
		t.Fatalf("closed list = %#v query=%s", list.PRs, query)
	}
	if strings.Contains(query, "reviewRequested:search") || strings.Contains(query, "reviewQuery=") {
		t.Fatalf("closed list requested open review data: %s", query)
	}
}

func TestListStateCachesRepositoryIdentity(t *testing.T) {
	repoCalls := 0
	client := Client{repo: &repositoryIdentity{}, run: func(args ...string) ([]byte, error) {
		if args[0] == "repo" {
			repoCalls++
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		}
		return []byte(`{"data":{"viewer":{"login":"octocat"},"reviewRequested":{"nodes":[],"pageInfo":{"hasNextPage":false}},"repository":{"pullRequests":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}`), nil
	}}
	if _, err := client.ListOpen(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListOpen(); err != nil {
		t.Fatal(err)
	}
	if repoCalls != 1 {
		t.Fatalf("gh repo view calls = %d, want 1", repoCalls)
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

func TestFindPreviewLoadsExpensiveFields(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		return []byte(`{"number":12,"body":"body","comments":[{"author":{"login":"alice"},"body":"review","createdAt":"2026-08-10T00:00:00Z"}],"commits":[{"oid":"a"},{"oid":"b"}],"statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`), nil
	}}
	pr, err := client.FindPreview(12)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Body != "body" || len(pr.Conversation) != 1 || pr.CommentCount != 1 || pr.CommitCount != 2 || len(pr.Checks) != 1 || !pr.PreviewLoaded {
		t.Fatalf("preview = %#v", pr)
	}
}

func TestListOpenPaginatesPullRequests(t *testing.T) {
	var apiCalls int
	client := Client{run: func(args ...string) ([]byte, error) {
		if args[0] == "repo" {
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		}
		apiCalls++
		if strings.Contains(strings.Join(args, " "), "after=cursor-1") {
			return []byte(`{"data":{"viewer":{"login":"octocat"},"reviewRequested":{"nodes":[],"pageInfo":{"hasNextPage":false}},"repository":{"pullRequests":{"nodes":[{"number":2}],"pageInfo":{"hasNextPage":false}}}}}`), nil
		}
		return []byte(`{"data":{"viewer":{"login":"octocat"},"reviewRequested":{"nodes":[],"pageInfo":{"hasNextPage":false}},"repository":{"pullRequests":{"nodes":[{"number":1}],"pageInfo":{"hasNextPage":true,"endCursor":"cursor-1"}}}}}`), nil
	}}
	list, err := client.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	if apiCalls != 2 || len(list.PRs) != 2 || list.PRs[0].Number != 1 || list.PRs[1].Number != 2 {
		t.Fatalf("paginated PRs = calls:%d prs:%#v", apiCalls, list.PRs)
	}
}

func TestListOpenPaginatesReviewRequestsIndependently(t *testing.T) {
	var apiCalls int
	client := Client{run: func(args ...string) ([]byte, error) {
		if args[0] == "repo" {
			return []byte(`{"nameWithOwner":"acme/repo"}`), nil
		}
		apiCalls++
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "reviewAfter=review-1") {
			for _, arg := range args {
				if strings.HasPrefix(arg, "after=") {
					t.Fatalf("finished PR cursor was sent again: %s", joined)
				}
			}
			return []byte(`{"data":{"viewer":{"login":"octocat"},"reviewRequested":{"nodes":[{"number":2}],"pageInfo":{"hasNextPage":false}},"repository":{"pullRequests":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}`), nil
		}
		return []byte(`{"data":{"viewer":{"login":"octocat"},"reviewRequested":{"nodes":[{"number":1}],"pageInfo":{"hasNextPage":true,"endCursor":"review-1"}},"repository":{"pullRequests":{"nodes":[{"number":1},{"number":2}],"pageInfo":{"hasNextPage":false}}}}}`), nil
	}}
	list, err := client.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	if apiCalls != 2 || len(list.PRs) != 2 || !list.PRs[0].ViewerReviewRequested || !list.PRs[1].ViewerReviewRequested {
		t.Fatalf("review pagination = calls:%d prs:%#v", apiCalls, list.PRs)
	}
}

func TestListStateRejectsUnsupportedState(t *testing.T) {
	called := false
	c := Client{run: func(args ...string) ([]byte, error) {
		called = true
		return nil, nil
	}}
	if _, err := c.ListState("merged"); err == nil || called {
		t.Fatalf("ListState accepted unsupported state: err=%v called=%v", err, called)
	}
}

func TestListOpenFailure(t *testing.T) {
	c := Client{run: func(args ...string) ([]byte, error) { return []byte("offline"), errors.New("exit 1") }}
	if _, err := c.ListOpen(); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("ListOpen error = %v", err)
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
