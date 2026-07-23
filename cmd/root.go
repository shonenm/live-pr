// Package cmd wires the live-pr command-line interface.
package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:           "live-pr",
	Short:         "Living pull request for LLM-assisted development",
	Long:          "live-pr captures the decision/iteration timeline of an AI coding session\nand reviews it like a local GitHub pull request.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error { return rootCmd.Execute() }
