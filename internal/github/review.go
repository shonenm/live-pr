package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ReviewComment is one pending inline GitHub review comment.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// ReviewDraft is a local, unpublished GitHub pull-request review.
type ReviewDraft struct {
	Version  int             `json:"version"`
	PR       int             `json:"pr"`
	Commit   string          `json:"commit_id"`
	Body     string          `json:"body,omitempty"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// ReviewEvent is one GitHub review verdict.
type ReviewEvent string

const (
	ReviewCommentEvent        ReviewEvent = "COMMENT"
	ReviewApproveEvent        ReviewEvent = "APPROVE"
	ReviewRequestChangesEvent ReviewEvent = "REQUEST_CHANGES"
)

func NewReviewDraft(pr int, commit string) ReviewDraft {
	return ReviewDraft{Version: CacheVersion, PR: pr, Commit: commit}
}

func LoadReviewDraft(path string, pr int, commit string) (ReviewDraft, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewReviewDraft(pr, commit), nil
	}
	if err != nil {
		return ReviewDraft{}, err
	}
	var draft ReviewDraft
	if err := json.Unmarshal(data, &draft); err != nil {
		return ReviewDraft{}, fmt.Errorf("decode review draft: %w", err)
	}
	if draft.Version != CacheVersion {
		return ReviewDraft{}, fmt.Errorf("unsupported review draft version %d", draft.Version)
	}
	if draft.PR != pr || draft.Commit != commit {
		return NewReviewDraft(pr, commit), nil
	}
	return draft, nil
}

func SaveReviewDraft(path string, draft ReviewDraft) error {
	if err := ValidateReviewDraft(draft); err != nil {
		return err
	}
	draft.Version = CacheVersion
	return saveJSON(path, draft)
}

func ValidateReviewDraft(draft ReviewDraft) error {
	if draft.PR <= 0 {
		return errors.New("review requires a GitHub pull request")
	}
	if strings.TrimSpace(draft.Commit) == "" {
		return errors.New("review requires the pull request head commit")
	}
	for i := range draft.Comments {
		if err := ValidateReviewComment(draft.Comments[i]); err != nil {
			return fmt.Errorf("review comment %d: %w", i+1, err)
		}
	}
	return nil
}

func ValidateReviewComment(comment ReviewComment) error {
	if strings.TrimSpace(comment.Path) == "" {
		return errors.New("path must not be empty")
	}
	if comment.Line <= 0 {
		return errors.New("line must be greater than zero")
	}
	if comment.Side != "LEFT" && comment.Side != "RIGHT" {
		return errors.New("side must be LEFT or RIGHT")
	}
	if strings.TrimSpace(comment.Body) == "" {
		return errors.New("body must not be empty")
	}
	return nil
}

func ValidateReviewEvent(event ReviewEvent) error {
	switch event {
	case ReviewCommentEvent, ReviewApproveEvent, ReviewRequestChangesEvent:
		return nil
	default:
		return fmt.Errorf("unsupported review event %q", event)
	}
}
