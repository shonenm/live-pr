package cmd

import (
	"strings"
	"testing"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/spf13/cobra"
)

func TestCommentInputRejectsRoutineEventKindsAndUnknownAuthors(t *testing.T) {
	if err := validateCommentInput(event.Commit, "commit", "agent"); err == nil {
		t.Fatal("commit must not be accepted as a comment")
	}
	if err := validateCommentInput(event.Decision, "choice", "bot-name"); err == nil {
		t.Fatal("unknown author must not be accepted")
	}
	if err := validateCommentInput(event.Decision, "choice", "agent"); err != nil {
		t.Fatal(err)
	}
}

func TestReadTextFlagSupportsStdin(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("body", "", "")
	cmd.Flags().String("body-file", "", "")
	if err := cmd.Flags().Set("body-file", "-"); err != nil {
		t.Fatal(err)
	}
	cmd.SetIn(strings.NewReader("important rationale\n"))
	got, err := readTextFlag(cmd, "body", "body-file")
	if err != nil || got != "important rationale" {
		t.Fatalf("stdin body = %q, %v", got, err)
	}
}
