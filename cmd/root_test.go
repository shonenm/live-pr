package cmd

import "testing"

func TestCommandRegistration(t *testing.T) {
	commands := map[string]bool{}
	for _, command := range rootCmd.Commands() {
		commands[command.Name()] = true
	}
	for _, name := range []string{"append", "decision", "demo", "hook", "init", "note", "pivot", "pr", "sync"} {
		if !commands[name] {
			t.Errorf("top-level command %q is not registered", name)
		}
	}

	for _, name := range []string{"base", "draft", "dry-run", "force-managed-body"} {
		if prCmd.Flags().Lookup(name) == nil {
			t.Errorf("pr flag --%s is not registered", name)
		}
	}
	command, _, err := rootCmd.Find([]string{"hook", "stop"})
	if err != nil || command != hookStopCmd {
		t.Fatalf("hook stop lookup = %v, %v", command, err)
	}
	rootCmd.InitDefaultVersionFlag()
	if rootCmd.Flags().Lookup("version") == nil {
		t.Fatal("root --version flag is not registered")
	}
}
