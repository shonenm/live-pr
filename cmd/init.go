package cmd

import (
	"fmt"
	"os"

	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

const hookSnippet = `Add to your Claude Code settings (.claude/settings.json in the project,
or ~/.claude/settings.json) so each session is summarized into the timeline:

{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "live-pr hook stop" } ] }
    ]
  }
}`

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the live-pr store for the current branch",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if hooks, _ := cmd.Flags().GetBool("hooks"); hooks {
			fmt.Fprintln(cmd.OutOrStdout(), hookSnippet)
			return nil
		}
		st, err := store.Discover()
		if err != nil {
			return err
		}
		// Seed the final summary only if absent — never clobber existing work.
		if err := seedSummary(st); err != nil {
			return err
		}
		// Ensure the timeline file exists (empty is valid).
		f, err := os.OpenFile(st.Timeline(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		_ = f.Close()

		fmt.Fprintf(cmd.OutOrStdout(), "initialized %s\n", st.Dir)
		return nil
	},
}

func init() {
	initCmd.Flags().Bool("hooks", false, "print the Claude Code Stop hook config to install")
	rootCmd.AddCommand(initCmd)
}
