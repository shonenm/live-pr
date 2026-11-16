package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/v2/internal/config"
)

func TestLocalCIRunsFromChecksViewWithoutPR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs checks through sh")
	}
	for key, value := range map[string]string{
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull,
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(key, value)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	m := testModel()
	m.root = root
	m.localCIConfig = config.LocalCIConfig{Checks: map[string]string{"unit": "echo ok", "broken": "echo boom >&2; exit 1"}}
	if !m.canRunLocalCI() {
		t.Fatal("local CI is not runnable")
	}
	cmd := m.startLocalCI()
	if m.canRunLocalCI() {
		t.Fatal("a second run can start while one is running")
	}
	for m.localCI.running {
		switch msg := cmd().(type) {
		case localCIEvent:
			m.applyLocalCIEvent(msg.event)
			cmd = waitLocalCI(msg.events)
		case localCIDone:
			m.localCI.running, m.localCI.note = false, msg.note
		}
	}

	out, _ := m.buildChecks()
	plain := ansi.Strip(out)
	for _, want := range []string{"Local CI", "└─ ✓ echo ok · success", "└─ ✗ echo boom >&2; exit 1 · failure", "boom", "attested unit", "without a signature"} {
		if !strings.Contains(plain, want) {
			t.Errorf("checks view lacks %q:\n%s", want, plain)
		}
	}
}
