package tui

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points XDG_STATE_HOME at a throwaway directory so a test that
// forgets to isolate its store cannot write into the developer's real state.
func TestMain(m *testing.M) {
	// TestCITokenHelper re-runs this binary and exits from inside the test,
	// which would strand a directory created here.
	if os.Getenv("GO_WANT_CI_TOKEN_HELPER") != "" {
		os.Exit(m.Run())
	}
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
