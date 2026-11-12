package cmd

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points XDG_STATE_HOME at a throwaway directory so a test that
// forgets to isolate its store cannot write into the developer's real state.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "live-pr-test-state-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_STATE_HOME", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
