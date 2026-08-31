package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shonenm/live-pr/internal/atomicfile"
)

const CacheVersion = 1

var ErrInvalidCache = errors.New("invalid GitHub cache")

// Cache is branch-local mutable GitHub state. Remote resources intentionally
// stay separate from the append-only local timeline.
type Cache struct {
	Version                      int                   `json:"version"`
	Head                         string                `json:"head,omitempty"`
	PR                           *PR                   `json:"pr,omitempty"`
	ExplicitCheckout             bool                  `json:"explicit_checkout,omitempty"`
	Comments                     []Comment             `json:"comments,omitempty"`
	Activities                   []Activity            `json:"activities,omitempty"`
	Reviews                      []Review              `json:"reviews,omitempty"`
	ReviewComments               []ReviewThreadComment `json:"review_comments,omitempty"`
	FetchedAt                    string                `json:"fetched_at,omitempty"`
	LastPublishedManagedBodyHash string                `json:"last_published_managed_body_hash,omitempty"`
	Warning                      string                `json:"-"`
}

// NewCache returns an initialized empty cache.
func NewCache(head string) Cache { return Cache{Version: CacheVersion, Head: head} }

// Clone returns an async-safe snapshot whose pointers and slices do not share
// mutable backing storage with the live model.
func (c Cache) Clone() Cache {
	if c.PR != nil {
		pr := *c.PR
		pr.Conversation = append([]PRConversationComment(nil), pr.Conversation...)
		pr.Commits = append([]PRCommit(nil), pr.Commits...)
		pr.Checks = append([]PRCheck(nil), pr.Checks...)
		pr.Assignees = append([]PRUser(nil), pr.Assignees...)
		pr.Labels = append([]PRLabel(nil), pr.Labels...)
		pr.ReviewRequests = append([]PRUser(nil), pr.ReviewRequests...)
		pr.ClosingIssues = append([]IssueRef(nil), pr.ClosingIssues...)
		c.PR = &pr
	}
	c.Comments = append([]Comment(nil), c.Comments...)
	c.Activities = append([]Activity(nil), c.Activities...)
	c.Reviews = append([]Review(nil), c.Reviews...)
	c.ReviewComments = append([]ReviewThreadComment(nil), c.ReviewComments...)
	return c
}

// Base returns the bound PR base when known, otherwise fallback.
func (c Cache) Base(fallback string) string {
	if c.PR != nil && c.PR.BaseRefName != "" {
		return c.PR.BaseRefName
	}
	return fallback
}

// LoadCache loads path. A missing file is an empty cache, while malformed or
// unsupported data is reported so callers can keep running without trusting it.
func LoadCache(path, head string) (Cache, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewCache(head), nil
	}
	if err != nil {
		return Cache{}, err
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return Cache{}, fmt.Errorf("%w: decode %s: %w", ErrInvalidCache, path, err)
	}
	if c.Version != CacheVersion {
		// The cache is re-fetchable derived data; an unsupported version
		// resets it instead of blocking the caller, like a head mismatch.
		return NewCache(head), nil
	}
	if c.Head != "" && c.Head != head {
		return NewCache(head), nil
	}
	c.Head = head
	return c, nil
}

// SaveCache atomically replaces path with c.
func SaveCache(path string, c Cache) error {
	c.Version = CacheVersion
	return saveJSON(path, c)
}

func saveJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".github-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return atomicfile.Replace(name, path)
}
