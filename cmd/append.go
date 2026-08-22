package cmd

import (
	"fmt"
	"strings"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

// addEvent stamps and appends one event to the current branch's timeline.
func addEvent(cmd *cobra.Command, kind event.Kind, title, body, sha, author string) error {
	if err := validateEventInput(kind, title, author); err != nil {
		return err
	}
	st, err := store.Discover()
	if err != nil {
		return err
	}
	ev := event.New(kind, title, body)
	ev.SHA, ev.Author = sha, author
	created, err := event.Create(st.Timeline(), ev)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", created.ID, kind, title)
	return nil
}

var appendCmd = &cobra.Command{
	Use:   "append",
	Short: "Append a raw event to the timeline",
	RunE: func(cmd *cobra.Command, _ []string) error {
		kind, _ := cmd.Flags().GetString("kind")
		title, _ := cmd.Flags().GetString("title")
		body, _ := cmd.Flags().GetString("body")
		sha, _ := cmd.Flags().GetString("sha")
		author, _ := cmd.Flags().GetString("author")
		return addEvent(cmd, event.Kind(kind), title, body, sha, author)
	},
}

// wrapper builds a convenience subcommand for a fixed kind:
//
//	live-pr <name> <title...> [--body ...]
func wrapper(name string, kind event.Kind, short string) *cobra.Command {
	c := &cobra.Command{
		Use:   name + " <title>",
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, _ := cmd.Flags().GetString("body")
			author, _ := cmd.Flags().GetString("author")
			return addEvent(cmd, kind, strings.Join(args, " "), body, "", author)
		},
	}
	c.Flags().String("body", "", "optional detail body")
	c.Flags().String("author", "user", "comment author: user or agent")
	return c
}

func init() {
	appendCmd.Flags().String("kind", "note", "event kind: note|decision|pivot|summary|commit")
	appendCmd.Flags().String("title", "", "one-line title (required)")
	appendCmd.Flags().String("body", "", "optional detail body")
	appendCmd.Flags().String("sha", "", "commit sha (for commit events)")
	appendCmd.Flags().String("author", "user", "event author: user or agent")
	_ = appendCmd.MarkFlagRequired("title")

	rootCmd.AddCommand(
		appendCmd,
		wrapper("note", event.Note, "Append a note to the timeline"),
		wrapper("decision", event.Decision, "Record a decision on the timeline"),
		wrapper("pivot", event.Pivot, "Record a change in direction on the timeline"),
	)
}
