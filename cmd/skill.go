package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shonenm/live-pr/skills"
	"github.com/spf13/cobra"
)

func materializeSkill() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cache, "live-pr", "skills", "live-pr", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(skills.Markdown), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Expose the version-matched live-pr Agent Skill",
}

var skillPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Materialize the bundled Agent Skill and print its path",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := materializeSkill()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), path)
		return nil
	},
}

var skillPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print the bundled Agent Skill",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), skills.Markdown)
		return err
	},
}

func init() {
	skillCmd.AddCommand(skillPathCmd, skillPrintCmd)
	rootCmd.AddCommand(skillCmd)
}
