package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
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

	bodyFile, err := os.CreateTemp("", "live-pr-body-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(bodyFile.Name())
	if _, err := bodyFile.WriteString(body); err != nil {
		return err
	}
	bodyFile.Close()

	if err := git.Push(st.Branch); err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	if url := existingPRURL(st.Branch); url != "" {
		if err := exec.Command("gh", "pr", "edit", st.Branch, "--title", title, "--body-file", bodyFile.Name()).Run(); err != nil {
			return fmt.Errorf("gh pr edit: %w", err)
		}
		fmt.Println("updated", url)
		return nil
	}

	// gh --base wants a branch name; the comparison base may be a remote ref.
	prBase := strings.TrimPrefix(base, "origin/")
	args := []string{"pr", "create", "--base", prBase, "--head", st.Branch, "--title", title, "--body-file", bodyFile.Name()}
	if draft, _ := cmd.Flags().GetBool("draft"); draft {
		args = append(args, "--draft")
	}
	out, err := exec.Command("gh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr create: %w\n%s", err, out)
	}
	fmt.Print(string(out))
	return nil
}

// existingPRURL returns the URL of an open PR for branch, or "" if none.
func existingPRURL(branch string) string {
	out, err := exec.Command("gh", "pr", "view", branch, "--json", "url", "--jq", ".url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func init() {
	prCmd.Flags().String("base", "", "base branch (default: repo default branch)")
	prCmd.Flags().Bool("draft", false, "create as a draft PR")
	prCmd.Flags().Bool("dry-run", false, "print the assembled PR body instead of creating/updating")
	rootCmd.AddCommand(prCmd)
}
