package cmd

import (
	"fmt"

	"github.com/shonenm/live-pr/internal/demo"
	"github.com/spf13/cobra"
)

var demoCmd = &cobra.Command{
	Use:   "demo [git|delta|delta-side|codereview]",
	Short: "Open a disposable review with mocked GitHub actions",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		mode := "git"
		if len(args) == 1 {
			mode = args[0]
		}
		switch mode {
		case "git", "delta", "delta-side", "codereview":
		default:
			return fmt.Errorf("unknown demo mode %q (use git, delta, delta-side, or codereview)", mode)
		}
		return demo.Run(mode, version)
	},
}

func init() {
	rootCmd.AddCommand(demoCmd)
}
