// Package publish creates or updates the current branch's GitHub pull request.
package publish

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
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
	Update(int, string, string) error
	Create(string, string, string, string, bool) (string, error)
}

// BuildPreview assembles the current conclusion and timeline.
func BuildPreview(base string) (Preview, error) {
	st, err := store.Discover()
	if err != nil {
		return Preview{}, err
	}
	return buildPreview(st, base)
}

// buildPreview assembles the preview from an already-resolved store, so callers
// that have one need not re-discover it.
func buildPreview(st *store.Store, base string) (Preview, error) {
	base = git.ResolveBase(base)
	events, err := event.Load(st.Timeline())
	if err != nil {
		return Preview{}, err
	}
	commits, err := git.Commits(base)
	if err != nil {
		return Preview{}, err
	}
	events = timeline.WithCommits(events, commits)
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	conclusion, err := os.ReadFile(st.Conclusion())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Preview{}, err
	}
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
	st, err := store.Discover()
	if err != nil {
		return Result{}, err
	}
	cache, err := gh.LoadCache(st.GitHubCache(), st.Branch)
	if err != nil {
		return Result{}, err
	}
	pr, findErr := client.FindOpen(st.Branch)
	base, err := resolvePublishBase(opts.Base, cache, pr, findErr)
	if err != nil {
		return Result{}, err
	}
	if _, err := timeline.SyncCommits(st.Timeline(), git.ResolveBase(base)); err != nil {
		return Result{}, err
	}
	preview, err := buildPreview(st, base)
	if err != nil {
		return Result{}, err
	}
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

	pr, created, err := applyRemote(client, st.Branch, preview, body, bodyFile, pr, findErr, opts.Draft)
	if err != nil {
		return Result{}, err
	}

	cache.PR = &pr
	cache.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	cache.LastPublishedManagedBodyHash = prbody.ManagedHash(body)
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		return Result{}, err
	}
	return Result{PR: pr, Created: created}, nil
}

// resolvePublishBase picks the base ref: an open PR's base wins (and rejects a
// conflicting request), otherwise the requested base, otherwise the cache default.
func resolvePublishBase(requested string, cache gh.Cache, pr gh.PR, findErr error) (string, error) {
	if findErr == nil && pr.BaseRefName != "" {
		if req := normalizeBase(requested); req != "" && req != pr.BaseRefName {
			return "", fmt.Errorf("open PR base is %s, not %s", pr.BaseRefName, req)
		}
		return pr.BaseRefName, nil
	}
	if requested == "" {
		return cache.Base(git.DefaultBase()), nil
	}
	return requested, nil
}

// applyRemote creates or updates the PR and returns the resulting PR and whether
// it was newly created.
func applyRemote(client githubClient, branch string, preview Preview, body, bodyFile string, pr gh.PR, findErr error, draft bool) (gh.PR, bool, error) {
	if findErr == nil {
		if err := client.Update(pr.Number, preview.Title, bodyFile); err != nil {
			return gh.PR{}, false, err
		}
		pr.Title, pr.Body = preview.Title, body
		if pr.BaseRefName == "" {
			pr.BaseRefName = normalizeBase(preview.Base)
		}
		return pr, false, nil
	}
	base := normalizeBase(preview.Base)
	url, err := client.Create(base, branch, preview.Title, bodyFile, draft)
	if err != nil {
		return gh.PR{}, true, err
	}
	created, err := client.FindOpen(branch)
	if err != nil {
		// Create succeeded but the read-back failed: keep a usable PR shape,
		// recovering the number from the URL rather than leaving it at 0.
		created = gh.PR{URL: url, Number: prNumberFromURL(url), Title: preview.Title, Body: body, State: "OPEN"}
	}
	if created.BaseRefName == "" {
		created.BaseRefName = base
	}
	return created, true, nil
}

// prNumberFromURL extracts the trailing number from a .../pull/<n> URL, or 0.
func prNumberFromURL(url string) int {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(url[i+1:])
	if err != nil {
		return 0
	}
	return n
}

func normalizeBase(base string) string { return strings.TrimPrefix(base, "origin/") }

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
