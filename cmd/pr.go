package cmd

import (
	"fmt"

	"github.com/shonenm/live-pr/internal/publish"
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Export the timeline as a GitHub pull request (create or update)",
	RunE:  runPR,
}

func runPR(cmd *cobra.Command, _ []string) error {
	base, _ := cmd.Flags().GetString("base")
	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		preview, err := publish.BuildPreview(base)
		if err != nil {
			return err
		}
		fmt.Printf("# %s\n_(base: %s ← %s)_\n\n%s", preview.Title, preview.Base, preview.Head, preview.Body)
		return nil
	}
	draft, _ := cmd.Flags().GetBool("draft")
	force, _ := cmd.Flags().GetBool("force-managed-body")
	result, err := publish.Run(publish.Options{Base: base, Draft: draft, ForceManagedBody: force})
	if err != nil {
		return err
	}
	if result.Created {
		fmt.Println(result.PR.URL)
	} else {
		fmt.Println("updated", result.PR.URL)
	}
	return nil
}

func init() {
	prCmd.Flags().String("base", "", "base branch (default: repo default branch)")
	prCmd.Flags().Bool("draft", false, "create as a draft PR")
	prCmd.Flags().Bool("dry-run", false, "print the assembled PR body instead of creating/updating")
	prCmd.Flags().Bool("force-managed-body", false, "overwrite a conflicting live-pr managed block")
	rootCmd.AddCommand(prCmd)
}
