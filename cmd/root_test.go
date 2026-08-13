package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandRegistration(t *testing.T) {
	commands := map[string]bool{}
	for _, command := range rootCmd.Commands() {
		commands[command.Name()] = true
	}
	for _, name := range []string{"append", "comment", "decision", "demo", "hook", "init", "note", "pivot", "pr", "review", "skill", "status", "summary", "sync"} {
		if !commands[name] {
			t.Errorf("top-level command %q is not registered", name)
		}
	}

	for _, name := range []string{"base", "draft", "force-managed-body"} {
		if prCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("pr flag --%s is not registered", name)
		}
	}
	if prCmd.Flags().Lookup("dry-run") == nil {
		t.Errorf("pr flag --dry-run is not registered")
	}
	// Subcommands inherit the persistent flags.
	for _, sub := range []*cobra.Command{prPreviewCmd, prPublishCmd} {
		if sub.InheritedFlags().Lookup("base") == nil {
			t.Errorf("pr %s did not inherit --base", sub.Name())
		}
	}
	for path, want := range map[string]*cobra.Command{
		"comment add":    commentAddCmd,
		"comment delete": commentDeleteCmd,
		"comment edit":   commentEditCmd,
		"comment list":   commentListCmd,
		"hook stop":      hookStopCmd,
		"pr preview":     prPreviewCmd,
		"pr publish":     prPublishCmd,
		"review add":     reviewAddCmd,
		"review body":    reviewBodyCmd,
		"review clear":   reviewClearCmd,
		"review delete":  reviewDeleteCmd,
		"review show":    reviewShowCmd,
		"review submit":  reviewSubmitCmd,
		"skill path":     skillPathCmd,
		"skill print":    skillPrintCmd,
		"summary edit":   summaryEditCmd,
		"summary set":    summarySetCmd,
		"summary show":   summaryShowCmd,
	} {
		command, _, err := rootCmd.Find(strings.Fields(path))
		if err != nil || command != want {
			t.Fatalf("%s lookup = %v, %v", path, command, err)
		}
	}
	if rootCmd.PersistentFlags().Lookup("cwd") == nil {
		t.Fatal("root --cwd flag is not registered")
	}
	rootCmd.InitDefaultVersionFlag()
	if rootCmd.Flags().Lookup("version") == nil {
		t.Fatal("root --version flag is not registered")
	}
}
