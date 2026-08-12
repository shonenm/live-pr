package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

func currentReviewDraft() (*store.Store, gh.PR, gh.ReviewDraft, error) {
	st, err := store.Discover()
	if err != nil {
		return nil, gh.PR{}, gh.ReviewDraft{}, err
	}
	cache, err := gh.LoadCache(st.GitHubCache(), st.Branch)
	if err != nil {
		return nil, gh.PR{}, gh.ReviewDraft{}, err
	}
	var pr gh.PR
	if cache.PR != nil {
		pr = *cache.PR
	} else {
		pr, err = gh.New().FindForHead(st.Branch)
		if err != nil {
			return nil, gh.PR{}, gh.ReviewDraft{}, err
		}
	}
	if pr.Number <= 0 || strings.TrimSpace(pr.HeadRefOID) == "" {
		return nil, gh.PR{}, gh.ReviewDraft{}, errors.New("pull request head commit is unavailable; refresh the PR first")
	}
	draft, err := gh.LoadReviewDraft(store.PullRequestReviewDraft(st.Root, pr.Number), pr.Number, pr.HeadRefOID)
	return st, pr, draft, err
}

var reviewCmd = &cobra.Command{Use: "review", Short: "Draft and submit a GitHub pull request review"}

var reviewShowCmd = &cobra.Command{
	Use: "show", Short: "Show the pending review draft", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, _, draft, err := currentReviewDraft()
		if err != nil {
			return err
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(draft)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "PR #%d @ %s\n\n%s\n", draft.PR, draft.Commit, draft.Body)
		for i, comment := range draft.Comments {
			fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s:%d\t%s\t%s\n", i+1, comment.Path, comment.Line, comment.Side, comment.Body)
		}
		return nil
	},
}

var reviewBodyCmd = &cobra.Command{
	Use: "body", Short: "Set the pending general review body", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		body, err := readTextFlag(cmd, "body", "body-file")
		if err != nil {
			return err
		}
		st, _, draft, err := currentReviewDraft()
		if err != nil {
			return err
		}
		draft.Body = strings.TrimSpace(body)
		if err := gh.SaveReviewDraft(store.PullRequestReviewDraft(st.Root, draft.PR), draft); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), store.PullRequestReviewDraft(st.Root, draft.PR))
		return nil
	},
}

var reviewAddCmd = &cobra.Command{
	Use: "add <path>", Short: "Add an inline comment to the pending review", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		line, err := cmd.Flags().GetInt("line")
		if err != nil {
			return err
		}
		body, err := readTextFlag(cmd, "body", "body-file")
		if err != nil {
			return err
		}
		side, _ := cmd.Flags().GetString("side")
		comment := gh.ReviewComment{Path: args[0], Line: line, Side: strings.ToUpper(side), Body: body}
		if err := gh.ValidateReviewComment(comment); err != nil {
			return err
		}
		st, _, draft, err := currentReviewDraft()
		if err != nil {
			return err
		}
		draft.Comments = append(draft.Comments, comment)
		if err := gh.SaveReviewDraft(store.PullRequestReviewDraft(st.Root, draft.PR), draft); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), len(draft.Comments))
		return nil
	},
}

var reviewDeleteCmd = &cobra.Command{
	Use: "delete <index>", Short: "Delete one pending inline comment", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var index int
		if _, err := fmt.Sscan(args[0], &index); err != nil || index < 1 {
			return fmt.Errorf("invalid comment index %q", args[0])
		}
		st, _, draft, err := currentReviewDraft()
		if err != nil {
			return err
		}
		if index > len(draft.Comments) {
			return fmt.Errorf("review comment %d not found", index)
		}
		draft.Comments = append(draft.Comments[:index-1], draft.Comments[index:]...)
		if err := gh.SaveReviewDraft(store.PullRequestReviewDraft(st.Root, draft.PR), draft); err != nil {
			return err
		}
		return nil
	},
}

var reviewClearCmd = &cobra.Command{
	Use: "clear", Short: "Discard the pending review", Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		st, _, draft, err := currentReviewDraft()
		if err != nil {
			return err
		}
		if err := os.Remove(store.PullRequestReviewDraft(st.Root, draft.PR)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	},
}

var reviewSubmitCmd = &cobra.Command{
	Use: "submit", Short: "Submit the pending review to GitHub", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		event, _ := cmd.Flags().GetString("event")
		st, _, draft, err := currentReviewDraft()
		if err != nil {
			return err
		}
		if err := gh.New().SubmitReview(draft, gh.ReviewEvent(strings.ToUpper(event))); err != nil {
			return err
		}
		if err := os.Remove(store.PullRequestReviewDraft(st.Root, draft.PR)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "submitted %s review for PR #%d\n", strings.ToUpper(event), draft.PR)
		return nil
	},
}

func init() {
	reviewShowCmd.Flags().Bool("json", false, "output JSON")
	reviewBodyCmd.Flags().String("body", "", "general review body")
	reviewBodyCmd.Flags().String("body-file", "", "read body from a file, or - for stdin")
	reviewAddCmd.Flags().Int("line", 0, "line number in the pull request diff")
	reviewAddCmd.Flags().String("side", "RIGHT", "diff side: RIGHT for new code, LEFT for deleted code")
	reviewAddCmd.Flags().String("body", "", "inline comment body")
	reviewAddCmd.Flags().String("body-file", "", "read body from a file, or - for stdin")
	reviewSubmitCmd.Flags().String("event", string(gh.ReviewCommentEvent), "COMMENT, APPROVE, or REQUEST_CHANGES")
	reviewCmd.AddCommand(reviewShowCmd, reviewBodyCmd, reviewAddCmd, reviewDeleteCmd, reviewClearCmd, reviewSubmitCmd)
	rootCmd.AddCommand(reviewCmd)
}
