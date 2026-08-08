package github

import (
	"errors"
	"reflect"
	"strings"
	"testing"
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
