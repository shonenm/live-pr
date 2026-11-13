package cmd

import (
	"errors"
	"fmt"

	"github.com/shonenm/live-pr/v2/internal/config"
	"github.com/shonenm/live-pr/v2/internal/git"
	"github.com/shonenm/live-pr/v2/internal/localci"
	"github.com/spf13/cobra"
)

var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "Run checks locally and attest the result",
}

var ciRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the [ci.local.checks] commands in this checkout and sign an attestation",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := git.RepoRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		jobs := localci.Jobs(cfg.CI.Local.Checks)
		if len(jobs) == 0 {
			return errors.New("no local CI checks: set [ci.local.checks] in .live-pr.toml")
		}
		out := cmd.OutOrStdout()
		force, _ := cmd.Flags().GetBool("force")
		passed, note := localci.Execute(cmd.Context(), root, cfg.CI.SigningKey, git.DefaultBase(), force, jobs, func(ev localci.Event) {
			if ev.State == localci.StateRunning {
				return
			}
			_, _ = fmt.Fprintf(out, "%s: %s (%s)\n", ev.Job, ev.State, ev.Duration)
			if ev.Output != "" {
				_, _ = fmt.Fprintln(out, ev.Output)
			}
		})
		_, _ = fmt.Fprintln(out, note)
		if len(passed) != len(jobs) {
			return errors.New("local CI failed")
		}
		return nil
	},
}

var ciVerifyScriptCmd = &cobra.Command{
	Use:   "verify-script",
	Short: "Print the shell script a remote CI job runs to verify attestations",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), localci.VerifyScript)
		return err
	},
}

func init() {
	ciRunCmd.Flags().Bool("force", false, "run even when the tree is already attested")
	ciCmd.AddCommand(ciRunCmd, ciVerifyScriptCmd)
	rootCmd.AddCommand(ciCmd)
}
