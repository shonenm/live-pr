package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

const defaultConclusion = `# <title>

<current conclusion — overwrite this in place as the work evolves>
`

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
			fmt.Println(hookSnippet)
			return nil
		}
		st, err := store.Discover()
		if err != nil {
			return err
		}
		// Seed conclusion.md only if absent — never clobber an existing head.
		if _, err := os.Stat(st.Conclusion()); os.IsNotExist(err) {
			if err := os.WriteFile(st.Conclusion(), []byte(defaultConclusion), 0o644); err != nil {
				return err
			}
		}
		// Ensure the timeline file exists (empty is valid).
		f, err := os.OpenFile(st.Timeline(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		_ = f.Close()

		if err := ensureGitignored(st.Root); err != nil {
			return err
		}
		fmt.Printf("initialized %s\n", st.Dir)
		return nil
	},
}

// ensureGitignored adds ".live-pr/" to the repo .gitignore if not already present.
// The timeline is local runtime state; it is read at export time, not committed.
func ensureGitignored(root string) error {
	const entry = ".live-pr/"
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	_, err = fmt.Fprintf(f, "%s%s\n", prefix, entry)
	return err
}

func init() {
	initCmd.Flags().Bool("hooks", false, "print the Claude Code Stop hook config to install")
	rootCmd.AddCommand(initCmd)
}
