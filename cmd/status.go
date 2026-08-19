package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

type statusPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Draft  bool   `json:"draft"`
}

type statusOutput struct {
	Repository     string    `json:"repository"`
	Branch         string    `json:"branch"`
	Base           string    `json:"base"`
	Target         string    `json:"target"`
	Events         int       `json:"events"`
	HasConclusion  bool      `json:"hasConclusion"`
	StateDirectory string    `json:"stateDirectory"`
	CacheFetchedAt string    `json:"cacheFetchedAt,omitempty"`
	PR             *statusPR `json:"pr,omitempty"`
}

func loadStatus(refresh bool) (statusOutput, error) {
	st, cache, err := store.LoadSession()
	if err != nil {
		return statusOutput{}, err
	}
	if refresh {
		pr, findErr := gh.New().FindForHead(st.Branch)
		switch {
		case findErr == nil:
			cache.PR = &pr
		case errors.Is(findErr, gh.ErrPRNotFound):
			cache.PR = nil
		default:
			return statusOutput{}, findErr
		}
		cache.FetchedAt = time.Now().UTC().Format(time.RFC3339)
		if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
			return statusOutput{}, err
		}
	}
	events, err := event.Load(st.Timeline())
	if err != nil {
		return statusOutput{}, err
	}
	_, conclusionErr := os.Stat(st.Conclusion())
	if conclusionErr != nil && !errors.Is(conclusionErr, os.ErrNotExist) {
		return statusOutput{}, conclusionErr
	}
	out := statusOutput{
		Repository: st.Root, Branch: st.Branch, Base: cache.Base(git.DefaultBase()),
		Target: "local", Events: len(events), HasConclusion: conclusionErr == nil,
		StateDirectory: st.Dir, CacheFetchedAt: cache.FetchedAt,
	}
	if cache.PR != nil {
		pr := cache.PR
		out.Target = "github"
		out.PR = &statusPR{
			Number: pr.Number, URL: pr.URL, Title: pr.Title, State: pr.State,
			Base: pr.BaseRefName, Head: pr.HeadRefName, Draft: pr.IsDraft,
		}
	}
	return out, nil
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current local and GitHub pull request status",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		refresh, _ := cmd.Flags().GetBool("refresh")
		status, err := loadStatus(refresh)
		if err != nil {
			return err
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(status)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", status.Repository, status.Branch)
		fmt.Fprintf(cmd.OutOrStdout(), "target: %s\nbase: %s\nevents: %d\n", status.Target, status.Base, status.Events)
		if status.PR != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "pr: #%d %s (%s)\n%s\n", status.PR.Number, status.PR.Title, status.PR.State, status.PR.URL)
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().Bool("json", false, "output JSON")
	statusCmd.Flags().Bool("refresh", false, "refresh the current branch PR from GitHub")
	rootCmd.AddCommand(statusCmd)
}
