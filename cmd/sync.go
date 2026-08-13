package cmd

import (
	"fmt"

	"github.com/shonenm/live-pr/internal/git"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Import base..HEAD commits into the timeline (idempotent)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := store.Discover()
		if err != nil {
			return err
		}
		base, _ := cmd.Flags().GetString("base")
		if base == "" {
			base = git.DefaultBase()
		}
		n, err := timeline.SyncCommits(st.Timeline(), base)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "synced %d new commit(s) from %s..HEAD\n", n, base)
		return nil
	},
}

func init() {
	syncCmd.Flags().String("base", "", "base ref to diff from (default: repo default branch)")
	rootCmd.AddCommand(syncCmd)
}
