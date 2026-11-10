package cmd

import (
	"errors"
	"fmt"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/git"
	"github.com/shonenm/live-pr/internal/localci"
	"github.com/spf13/cobra"
)

var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "Run workflow jobs locally and attest the result",
}

var ciRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the [ci.local] jobs in this checkout and sign an attestation",
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
		local := cfg.CI.Local
		if len(local.Jobs) == 0 {
			return errors.New("no local CI jobs: set [ci.local] jobs in .live-pr.toml")
		}
		jobs, err := localci.Load(root, local.Workflow, local.Jobs, local.Uses)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		byID := map[string]localci.Job{}
		supported := 0
		for _, job := range jobs {
			byID[job.ID] = job
			if job.Unsupported != "" {
				_, _ = fmt.Fprintf(out, "%s: remote only (%s)\n", job.ID, job.Unsupported)
				continue
			}
			supported++
		}
		passed, note := localci.Execute(cmd.Context(), root, local.Workflow, cfg.CI.SigningKey, jobs, func(ev localci.Event) {
			if ev.State == localci.StateRunning {
				return
			}
			_, _ = fmt.Fprintf(out, "%s / %s: %s (%s)\n", ev.Job, byID[ev.Job].Steps[ev.Step].Name, ev.State, ev.Duration)
			if ev.Output != "" {
				_, _ = fmt.Fprintln(out, ev.Output)
			}
		})
		_, _ = fmt.Fprintln(out, note)
		if len(passed) != supported {
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
	ciCmd.AddCommand(ciRunCmd, ciVerifyScriptCmd)
	rootCmd.AddCommand(ciCmd)
}
