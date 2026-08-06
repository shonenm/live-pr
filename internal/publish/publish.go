// Package publish creates or updates the current branch's GitHub pull request.
package publish

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prbody"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
)

const maxBodyBytes = 65536

// Options controls an explicit PR publish operation.
type Options struct {
	Base             string
	Draft            bool
	ForceManagedBody bool
}

// Preview is the assembled local PR content before remote merge.
type Preview struct {
	Title, Body, Base, Head string
}

// Result describes a completed remote publish.
type Result struct {
	PR      gh.PR
	Created bool
}

type githubClient interface {
	FindOpen(string) (gh.PR, error)
	Update(string, string, string) error
	Create(string, string, string, string, bool) (string, error)
}

// BuildPreview assembles the current conclusion and timeline.
func BuildPreview(base string) (Preview, error) {
	st, err := store.Discover()
	if err != nil {
		return Preview{}, err
	}
	if base == "" {
		base = git.DefaultBase()
	}
	_, _ = timeline.SyncCommits(st.Timeline(), base)
	events, err := event.Load(st.Timeline())
	if err != nil {
		return Preview{}, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	conclusion, _ := os.ReadFile(st.Conclusion())
	return Preview{
		Title: prbody.Title(string(conclusion), st.Branch),
		Body:  prbody.Render(string(conclusion), events),
		Base:  base,
		Head:  st.Branch,
	}, nil
}

// Run explicitly pushes the branch and creates or updates its PR.
func Run(opts Options) (Result, error) {
	client := gh.New()
	return run(opts, client, git.Push)
}

func run(opts Options, client githubClient, push func(string) error) (Result, error) {
	preview, err := BuildPreview(opts.Base)
	if err != nil {
		return Result{}, err
	}
	st, err := store.Discover()
	if err != nil {
		return Result{}, err
	}
	cache, err := gh.LoadCache(st.GitHubCache(), st.Branch)
	if err != nil {
		return Result{}, err
	}
	pr, findErr := client.FindOpen(st.Branch)
	body := preview.Body
	if findErr == nil {
		expected := cache.LastPublishedManagedBodyHash
		if opts.ForceManagedBody {
			expected = prbody.ManagedHash(pr.Body)
		}
		body, err = prbody.Merge(pr.Body, body, expected)
		if err != nil {
			return Result{}, err
		}
	} else if !errors.Is(findErr, gh.ErrPRNotFound) {
		return Result{}, findErr
	}
	if len(body) > maxBodyBytes {
		return Result{}, fmt.Errorf("PR body is %d bytes; GitHub limit is %d", len(body), maxBodyBytes)
	}

	bodyFile, err := writeBodyFile(body)
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(bodyFile)
	if err := push(st.Branch); err != nil {
		return Result{}, fmt.Errorf("git push: %w", err)
	}

	created := findErr != nil
	if !created {
		if err := client.Update(st.Branch, preview.Title, bodyFile); err != nil {
			return Result{}, err
		}
		pr.Title, pr.Body = preview.Title, body
	} else {
		base := strings.TrimPrefix(preview.Base, "origin/")
		url, err := client.Create(base, st.Branch, preview.Title, bodyFile, opts.Draft)
		if err != nil {
			return Result{}, err
		}
		if pr, err = client.FindOpen(st.Branch); err != nil {
			pr = gh.PR{URL: url, Title: preview.Title, Body: body, State: "OPEN"}
		}
	}

	cache.PR = &pr
	cache.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	cache.LastPublishedManagedBodyHash = prbody.ManagedHash(body)
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		return Result{}, err
	}
	return Result{PR: pr, Created: created}, nil
}

func writeBodyFile(body string) (string, error) {
	f, err := os.CreateTemp("", "live-pr-body-*.md")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
