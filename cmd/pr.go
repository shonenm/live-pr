package cmd

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
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Export the timeline as a GitHub pull request (create or update)",
	RunE:  runPR,
}

func runPR(cmd *cobra.Command, _ []string) error {
	st, err := store.Discover()
	if err != nil {
		return err
	}

	base, _ := cmd.Flags().GetString("base")
	if base == "" {
		base = git.DefaultBase()
	}
	// Fold in the latest commits so the PR body is complete.
	_, _ = timeline.SyncCommits(st.Timeline(), base)

	events, err := event.Load(st.Timeline())
	if err != nil {
		return err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })

	conclusionBytes, _ := os.ReadFile(st.Conclusion())
	conclusion := string(conclusionBytes)
	title := prbody.Title(conclusion, st.Branch)
	body := prbody.Render(conclusion, events)

	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		fmt.Printf("# %s\n_(base: %s ← %s)_\n\n%s", title, base, st.Branch, body)
		return nil
	}

	cache, err := gh.LoadCache(st.GitHubCache(), st.Branch)
	if err != nil {
		return err
	}
	client := gh.New()
	pr, findErr := client.FindOpen(st.Branch)
	if findErr == nil {
		expected := cache.LastPublishedManagedBodyHash
		if force, _ := cmd.Flags().GetBool("force-managed-body"); force {
			expected = prbody.ManagedHash(pr.Body)
		}
		body, err = prbody.Merge(pr.Body, body, expected)
		if err != nil {
			return err
		}
	} else if !errors.Is(findErr, gh.ErrPRNotFound) {
		return findErr
	}
	if len(body) > 65536 {
		return fmt.Errorf("PR body is %d bytes; GitHub limit is 65536", len(body))
	}

	bodyFile, err := writeBodyFile(body)
	if err != nil {
		return err
	}
	defer os.Remove(bodyFile)

	if err := git.Push(st.Branch); err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	if findErr == nil {
		if err := client.Update(st.Branch, title, bodyFile); err != nil {
			return err
		}
		pr.Title, pr.Body = title, body
		cache.PR = &pr
		cache.FetchedAt = time.Now().UTC().Format(time.RFC3339)
		cache.LastPublishedManagedBodyHash = prbody.ManagedHash(body)
		if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
			return err
		}
		fmt.Println("updated", pr.URL)
		return nil
	}

	// gh --base wants a branch name; the comparison base may be a remote ref.
	prBase := strings.TrimPrefix(base, "origin/")
	draft, _ := cmd.Flags().GetBool("draft")
	out, err := client.Create(prBase, st.Branch, title, bodyFile, draft)
	if err != nil {
		return err
	}
	created, refreshErr := client.FindOpen(st.Branch)
	if refreshErr != nil {
		created = gh.PR{URL: out, Title: title, Body: body, State: "OPEN"}
	}
	cache.PR = &created
	cache.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	cache.LastPublishedManagedBodyHash = prbody.ManagedHash(body)
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		return err
	}
	fmt.Println(out)
	return nil
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

func init() {
	prCmd.Flags().String("base", "", "base branch (default: repo default branch)")
	prCmd.Flags().Bool("draft", false, "create as a draft PR")
	prCmd.Flags().Bool("dry-run", false, "print the assembled PR body instead of creating/updating")
	prCmd.Flags().Bool("force-managed-body", false, "overwrite a conflicting live-pr managed block")
	rootCmd.AddCommand(prCmd)
}
