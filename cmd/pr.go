package cmd

import (
	"fmt"

	"github.com/shonenm/live-pr/internal/publish"
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Preview or publish the timeline as a GitHub pull request",
	Long:  "Preview or publish the timeline as a GitHub pull request. With no subcommand, pr publishes for backward compatibility.",
	RunE:  runPR,
}

func runPreview(cmd *cobra.Command) error {
	base, _ := cmd.Flags().GetString("base")
	preview, err := publish.BuildPreview(base)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "# %s\n_(base: %s ← %s)_\n\n%s", preview.Title, preview.Base, preview.Head, preview.Body)
	return nil
}

func runPublish(cmd *cobra.Command) error {
	base, _ := cmd.Flags().GetString("base")
	draft, _ := cmd.Flags().GetBool("draft")
	force, _ := cmd.Flags().GetBool("force-managed-body")
	result, err := publish.Run(publish.Options{Base: base, Draft: draft, ForceManagedBody: force})
	if err != nil {
		return err
	}
	if result.Created {
		fmt.Fprintln(cmd.OutOrStdout(), result.PR.URL)
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "updated", result.PR.URL)
	}
	return nil
}

func runPR(cmd *cobra.Command, _ []string) error {
	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		return runPreview(cmd)
	}
	return runPublish(cmd)
}

var prPreviewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Print the assembled pull request without publishing",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runPreview(cmd)
	},
}

var prPublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Push and create or update the pull request",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runPublish(cmd)
	},
}

func init() {
	prCmd.Flags().String("base", "", "base branch (default: repo default branch)")
	prCmd.Flags().Bool("draft", false, "create as a draft PR")
	prCmd.Flags().Bool("dry-run", false, "print the assembled PR body instead of creating/updating")
	prCmd.Flags().Bool("force-managed-body", false, "overwrite a conflicting live-pr managed block")

	prPreviewCmd.Flags().String("base", "", "base branch (default: repo default branch)")
	prPublishCmd.Flags().String("base", "", "base branch (default: repo default branch)")
	prPublishCmd.Flags().Bool("draft", false, "create as a draft PR")
	prPublishCmd.Flags().Bool("force-managed-body", false, "overwrite a conflicting live-pr managed block")
	prCmd.AddCommand(prPreviewCmd, prPublishCmd)
	rootCmd.AddCommand(prCmd)
}
