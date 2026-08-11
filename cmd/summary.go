package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/shonenm/live-pr/internal/prtemplate"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

func seedSummary(st *store.Store) error { return prtemplate.Seed(st) }

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Manage the final pull request summary",
}

var summaryShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the current final summary",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := store.Discover()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(st.Conclusion())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(body)
		return err
	},
}

var summarySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Replace the final summary from text, a file, or stdin",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("body") && !cmd.Flags().Changed("file") {
			return fmt.Errorf("provide --body or --file")
		}
		body, err := readTextFlag(cmd, "body", "file")
		if err != nil {
			return err
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("summary must not be empty")
		}
		st, err := store.Discover()
		if err != nil {
			return err
		}
		if err := st.WriteConclusion(body); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), st.Conclusion())
		return nil
	},
}

var summaryEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open the final summary in $VISUAL or $EDITOR",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := store.Discover()
		if err != nil {
			return err
		}
		if err := seedSummary(st); err != nil {
			return err
		}
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		parts := strings.Fields(editor)
		if len(parts) == 0 {
			return fmt.Errorf("set $VISUAL or $EDITOR to edit the summary")
		}
		process := exec.Command(parts[0], append(parts[1:], st.Conclusion())...)
		process.Stdin, process.Stdout, process.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		return process.Run()
	},
}

func init() {
	summarySetCmd.Flags().String("body", "", "final summary Markdown")
	summarySetCmd.Flags().String("file", "", "read final summary from a file, or - for stdin")
	summaryCmd.AddCommand(summaryShowCmd, summarySetCmd, summaryEditCmd)
	rootCmd.AddCommand(summaryCmd)
}
