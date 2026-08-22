package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/spf13/cobra"
)

func validateEventInput(kind event.Kind, title, author string) error {
	switch kind {
	case event.Note, event.Decision, event.Pivot, event.Summary, event.Commit:
	default:
		return fmt.Errorf("unsupported event kind %q", kind)
	}
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("title must not be empty")
	}
	if author != "" && author != "user" && author != "agent" {
		return fmt.Errorf("author must be user or agent")
	}
	return nil
}

func validateCommentInput(kind event.Kind, title, author string) error {
	if kind != event.Note && kind != event.Decision && kind != event.Pivot {
		return fmt.Errorf("comment kind must be note, decision, or pivot")
	}
	return validateEventInput(kind, title, author)
}

func readTextFlag(cmd *cobra.Command, inlineName, fileName string) (string, error) {
	inline, _ := cmd.Flags().GetString(inlineName)
	file, _ := cmd.Flags().GetString(fileName)
	if file == "" {
		return inline, nil
	}
	if cmd.Flags().Changed(inlineName) {
		return "", fmt.Errorf("use only --%s or --%s", inlineName, fileName)
	}
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func currentTimeline() (string, []event.Event, error) {
	st, err := store.Discover()
	if err != nil {
		return "", nil, err
	}
	events, err := event.Load(st.Timeline())
	return st.Timeline(), events, err
}

func findEvent(events []event.Event, id string) (event.Event, error) {
	for _, ev := range events {
		if ev.ID == id {
			return ev, nil
		}
	}
	return event.Event{}, fmt.Errorf("timeline event %q not found", id)
}

var commentCmd = &cobra.Command{
	Use:   "comment",
	Short: "Manage reviewer-focused local PR comments",
}

var commentAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Add a decision, pivot, or significant note",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kind, _ := cmd.Flags().GetString("kind")
		author, _ := cmd.Flags().GetString("author")
		body, err := readTextFlag(cmd, "body", "body-file")
		if err != nil {
			return err
		}
		if err := validateCommentInput(event.Kind(kind), args[0], author); err != nil {
			return err
		}
		return addEvent(cmd, event.Kind(kind), args[0], body, "", author)
	},
}

var commentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List local PR comments",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, events, err := currentTimeline()
		if err != nil {
			return err
		}
		comments := make([]event.Event, 0, len(events))
		for _, ev := range events {
			if ev.Kind != event.Commit {
				comments = append(comments, ev)
			}
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(comments)
		}
		for _, ev := range comments {
			author := ev.Author
			if author == "" {
				author = "unknown"
			}
			edited := ""
			if ev.UpdatedAt != "" {
				edited = "\tedited"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s%s\n", ev.ID, ev.Kind, author, ev.Title, edited)
		}
		return nil
	},
}

var commentEditCmd = &cobra.Command{
	Use:   "edit <id> [title]",
	Short: "Edit a local PR comment without rewriting its history",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, events, err := currentTimeline()
		if err != nil {
			return err
		}
		current, err := findEvent(events, args[0])
		if err != nil {
			return err
		}
		if current.Kind == event.Commit {
			return fmt.Errorf("commit events cannot be edited")
		}
		changed := len(args) == 2 || cmd.Flags().Changed("kind") || cmd.Flags().Changed("author") || cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")
		if !changed {
			return fmt.Errorf("provide a title or a field to edit")
		}
		if len(args) == 2 {
			current.Title = args[1]
		}
		if cmd.Flags().Changed("kind") {
			kind, _ := cmd.Flags().GetString("kind")
			current.Kind = event.Kind(kind)
		}
		if cmd.Flags().Changed("author") {
			current.Author, _ = cmd.Flags().GetString("author")
		}
		if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
			current.Body, err = readTextFlag(cmd, "body", "body-file")
			if err != nil {
				return err
			}
		}
		if current.Kind == event.Summary {
			err = validateEventInput(current.Kind, current.Title, current.Author)
		} else {
			err = validateCommentInput(current.Kind, current.Title, current.Author)
		}
		if err != nil {
			return err
		}
		updated, err := event.Update(path, current.ID, current)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", updated.ID, updated.Kind, updated.Title)
		return nil
	},
}

var commentDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a local PR comment without rewriting its history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, events, err := currentTimeline()
		if err != nil {
			return err
		}
		current, err := findEvent(events, args[0])
		if err != nil {
			return err
		}
		if current.Kind == event.Commit {
			return fmt.Errorf("commit events cannot be deleted")
		}
		if err := event.Delete(path, current.ID); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), current.ID)
		return nil
	},
}

func init() {
	commentAddCmd.Flags().String("kind", "note", "comment kind: note, decision, or pivot")
	commentAddCmd.Flags().String("body", "", "optional Markdown detail")
	commentAddCmd.Flags().String("body-file", "", "read Markdown detail from a file, or - for stdin")
	commentAddCmd.Flags().String("author", "user", "comment author: user or agent")
	commentListCmd.Flags().Bool("json", false, "output JSON")
	commentEditCmd.Flags().String("kind", "", "replacement kind: note, decision, or pivot")
	commentEditCmd.Flags().String("body", "", "replacement Markdown detail; empty clears it")
	commentEditCmd.Flags().String("body-file", "", "read replacement detail from a file, or - for stdin")
	commentEditCmd.Flags().String("author", "", "replacement author: user or agent")
	commentCmd.AddCommand(commentAddCmd, commentListCmd, commentEditCmd, commentDeleteCmd)
	rootCmd.AddCommand(commentCmd)
}
