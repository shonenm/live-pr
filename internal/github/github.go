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
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
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
func (c Client) FindOpen(head string) (PR, error) {
	out, err := c.run("pr", "list", "--head", head, "--state", "open", "--limit", "1", "--json", "number,url,title,body,state")
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
