package github

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReviewDraftRoundTripAndRevisionIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	draft := NewReviewDraft(12, "abc123")
	draft.Body = "Please address the notes."
	draft.Comments = []ReviewComment{{Path: "main.go", Line: 9, Side: "RIGHT", Body: "Handle this error."}}
	if err := SaveReviewDraft(path, draft); err != nil {
		t.Fatal(err)
	}
	got, err := LoadReviewDraft(path, 12, "abc123")
	if err != nil || got.Body != draft.Body || len(got.Comments) != 1 || got.Comments[0].Line != 9 {
		t.Fatalf("draft = %#v err=%v", got, err)
	}
	other, err := LoadReviewDraft(path, 12, "def456")
	if err != nil || other.Commit != "def456" || other.Body != "" || len(other.Comments) != 0 {
		t.Fatalf("revision-isolated draft = %#v err=%v", other, err)
	}
}

func TestReviewDraftValidation(t *testing.T) {
	for name, draft := range map[string]ReviewDraft{
		"missing PR":     NewReviewDraft(0, "abc"),
		"missing commit": NewReviewDraft(1, ""),
		"bad line":       {PR: 1, Commit: "abc", Comments: []ReviewComment{{Path: "x.go", Side: "RIGHT", Body: "x"}}},
		"bad side":       {PR: 1, Commit: "abc", Comments: []ReviewComment{{Path: "x.go", Line: 1, Side: "MIDDLE", Body: "x"}}},
		"empty body":     {PR: 1, Commit: "abc", Comments: []ReviewComment{{Path: "x.go", Line: 1, Side: "RIGHT"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateReviewDraft(draft); err == nil {
				t.Fatalf("accepted %#v", draft)
			}
		})
	}
	if err := ValidateReviewEvent("MERGE"); err == nil {
		t.Fatal("unsupported verdict accepted")
	}
}

func TestSubmitReviewSendsOneAtomicPayload(t *testing.T) {
	var gotArgs []string
	var payload struct {
		CommitID string          `json:"commit_id"`
		Body     string          `json:"body"`
		Event    ReviewEvent     `json:"event"`
		Comments []ReviewComment `json:"comments"`
	}
	client := Client{run: func(args ...string) ([]byte, error) {
		if args[0] == "pr" {
			return []byte(`{"number":12,"headRefOid":"abc123"}`), nil
		}
		gotArgs = append([]string(nil), args...)
		input := args[len(args)-1]
		data, err := os.ReadFile(input)
		if err != nil {
			return nil, err
		}
		return nil, json.Unmarshal(data, &payload)
	}}
	draft := NewReviewDraft(12, "abc123")
	draft.Body = "Please update this."
	draft.Comments = []ReviewComment{{Path: "main.go", Line: 4, Side: "RIGHT", Body: "Check the error."}}
	if err := client.SubmitReview(draft, ReviewRequestChangesEvent); err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "--method", "POST", "repos/{owner}/{repo}/pulls/12/reviews", "--input"}
	if len(gotArgs) != len(want)+1 || !reflect.DeepEqual(gotArgs[:len(want)], want) || payload.CommitID != "abc123" || payload.Event != ReviewRequestChangesEvent || len(payload.Comments) != 1 {
		t.Fatalf("args=%v payload=%#v", gotArgs, payload)
	}
}

func TestSubmitReviewValidatesAndPreservesCommandError(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		if args[0] == "pr" {
			return []byte(`{"number":12,"headRefOid":"abc123"}`), nil
		}
		return []byte("review rejected"), errors.New("exit 1")
	}}
	draft := NewReviewDraft(12, "abc123")
	if err := client.SubmitReview(draft, ReviewRequestChangesEvent); err == nil || !strings.Contains(err.Error(), "requires a review body") {
		t.Fatalf("empty changes request error = %v", err)
	}
	draft.Body = "changes needed"
	if err := client.SubmitReview(draft, ReviewRequestChangesEvent); err == nil || !strings.Contains(err.Error(), "review rejected") {
		t.Fatalf("submit error = %v", err)
	}
}

func TestSubmitReviewRejectsChangedHead(t *testing.T) {
	client := Client{run: func(args ...string) ([]byte, error) {
		return []byte(`{"number":12,"headRefOid":"new-head"}`), nil
	}}
	draft := NewReviewDraft(12, "old-head")
	draft.Body = "reviewed old code"
	if err := client.SubmitReview(draft, ReviewCommentEvent); err == nil || !strings.Contains(err.Error(), "head changed") {
		t.Fatalf("changed head error = %v", err)
	}
}

func TestLoadReviewDraftRejectsMalformedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReviewDraft(path, 1, "abc"); err == nil {
		t.Fatal("malformed draft accepted")
	}
}
