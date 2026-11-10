package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
)

func TestLocalCIRunsFromChecksViewWithoutPR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs steps through bash")
	}
	root := t.TempDir()
	workflow := "jobs:\n  test:\n    steps:\n      - uses: actions/checkout@v7\n      - name: unit\n        run: echo ok\n      - name: broken\n        run: echo boom >&2; exit 1\n  build:\n    strategy: {matrix: {os: [a]}}\n    steps:\n      - run: echo\n"
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	m := testModel()
	m.root = root
	m.localCIConfig = config.LocalCIConfig{Workflow: ".github/workflows/ci.yml", Jobs: []string{"test", "build"}}
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
	for _, want := range []string{"Local CI", "└─ ✓ unit · success", "└─ ✗ broken · failure", "boom", "build · remote only · job uses a strategy matrix", "not attested: no job passed"} {
		if !strings.Contains(plain, want) {
			t.Errorf("checks view lacks %q:\n%s", want, plain)
		}
	}
}
