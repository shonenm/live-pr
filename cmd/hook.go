package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/hook"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/summarize"
	"github.com/shonenm/live-pr/internal/transcript"
	"github.com/spf13/cobra"
)

const maxTranscriptBytes = 40 * 1024

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Agent hook entrypoints",
}

var hookStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Claude Code Stop hook: summarize the session into the timeline",
	// Always returns nil: a hook must never block or fail the agent.
	RunE: func(_ *cobra.Command, _ []string) error {
		var payload struct {
			TranscriptPath string `json:"transcript_path"`
			Cwd            string `json:"cwd"`
		}
		_ = json.NewDecoder(os.Stdin).Decode(&payload)
		if payload.Cwd != "" {
			_ = os.Chdir(payload.Cwd)
		}

		st, err := store.Discover()
		if err != nil {
			return nil // not a git repo → nothing to summarize
		}
		text, err := transcript.Text(payload.TranscriptPath, maxTranscriptBytes)
		if err != nil || text == "" {
			return nil
		}

		cfg, err := config.Load(st.Root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "live-pr hook stop:", err)
			return nil
		}
		added, err := hook.Stop(text, hook.Deps{
			TimelinePath: st.Timeline(),
			Summarizer:   summarize.Claude{Model: cfg.SummarizeModel},
			Now:          time.Now(),
			MinInterval:  cfg.SummaryInterval(),
		})
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "live-pr hook stop:", err)
		case added:
			fmt.Fprintln(os.Stderr, "live-pr: session summary added to timeline")
		}
		return nil
	},
}

func init() {
	hookCmd.AddCommand(hookStopCmd)
	rootCmd.AddCommand(hookCmd)
}
