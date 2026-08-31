package cmd

import (
	"fmt"
	"io"

	"github.com/shonenm/live-pr/internal/git"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Import base..HEAD commits into the timeline (idempotent)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		base, _ := cmd.Flags().GetString("base")
		return runSync(cmd.OutOrStdout(), base)
	},
}

func runSync(out io.Writer, requestedBase string) error {
	st, err := store.Discover()
	if err != nil {
		return err
	}
	base := requestedBase
	if base == "" {
		cache, cacheErr := st.LoadGitHubCache()
		if cacheErr != nil {
			return cacheErr
		}
		base = cache.Base(git.DefaultBase())
	}
	resolved := git.ResolveBase(base)
	n, err := timeline.SyncCommits(st.Timeline(), resolved)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "synced %d new commit(s) from %s..HEAD\n", n, base)
	return nil
}

func init() {
	syncCmd.Flags().String("base", "", "base ref to diff from (default: PR/cache base, then repo default branch)")
	rootCmd.AddCommand(syncCmd)
}
