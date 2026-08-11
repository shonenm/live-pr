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
	for _, name := range []string{"append", "decision", "demo", "hook", "init", "note", "pivot", "pr", "status", "sync"} {
		if !commands[name] {
			t.Errorf("top-level command %q is not registered", name)
		}
	}

	for _, name := range []string{"base", "draft", "dry-run", "force-managed-body"} {
		if prCmd.Flags().Lookup(name) == nil {
			t.Errorf("pr flag --%s is not registered", name)
		}
	}
	for path, want := range map[string]*cobra.Command{
		"hook stop":  hookStopCmd,
		"pr preview": prPreviewCmd,
		"pr publish": prPublishCmd,
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
