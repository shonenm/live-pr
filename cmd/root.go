// Package cmd wires the live-pr command-line interface.
package cmd

import (
	"fmt"
	"os"

	"github.com/shonenm/live-pr/internal/tui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "live-pr",
	Version:       version,
	Short:         "Living pull request for LLM-assisted development",
	Long:          "live-pr captures the decision/iteration timeline of an AI coding session\nand reviews it like a local GitHub pull request.",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		cwd, _ := cmd.Flags().GetString("cwd")
		if cwd == "" {
			return nil
		}
		if err := os.Chdir(cwd); err != nil {
			return fmt.Errorf("change directory to %s: %w", cwd, err)
		}
		return nil
	},
	// No subcommand → open the TUI for the current branch.
	RunE: func(_ *cobra.Command, _ []string) error {
		return tui.Run()
	},
}

func init() {
	rootCmd.PersistentFlags().StringP("cwd", "C", "", "run as if started in this directory")
}

// Execute runs the root command.
func Execute() error { return rootCmd.Execute() }
