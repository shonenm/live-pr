package cmd

import (
	"fmt"
	"io"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
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
	base := requestedBase
	var st *store.Store
	var err error
	if base == "" {
		var cache gh.Cache
		st, cache, err = store.LoadSession()
		if err == nil {
			base = cache.Base(git.DefaultBase())
		}
	} else {
		st, err = store.Discover()
	}
	if err != nil {
		return err
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
