// Package demo builds the disposable git repository, mock GitHub origin, and
// fake gh binary behind the live-pr demo command.
package demo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/shonenm/live-pr/internal/tui"
)

// Run builds a disposable demo repository with mocked GitHub data and opens
// the TUI inside it. The fixture is removed when the TUI exits.
func Run(mode, version string) error {
	root, err := os.MkdirTemp("", "live-pr-demo-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()

	if err := CreateRepo(root, mode); err != nil {
		return err
	}
	mockBin, mockState, err := SetupGitHub(root, mode)
	if err != nil {
		return err
	}
	oldDir, err := os.Getwd()
	if err != nil {
		return err
	}
	defer func() { _ = os.Chdir(oldDir) }()
	if err := os.Chdir(root); err != nil {
		return err
	}
	oldState, hadState := os.LookupEnv("XDG_STATE_HOME")
	oldDemoState, hadDemoState := os.LookupEnv("LIVE_PR_DEMO_STATE")
	oldPath := os.Getenv("PATH")
	defer restoreEnv("XDG_STATE_HOME", oldState, hadState)
	defer restoreEnv("LIVE_PR_DEMO_STATE", oldDemoState, hadDemoState)
	defer func() { _ = os.Setenv("PATH", oldPath) }()
	if err := os.Setenv("XDG_STATE_HOME", filepath.Join(root, ".state")); err != nil {
		return err
	}
	if err := os.Setenv("LIVE_PR_DEMO_STATE", mockState); err != nil {
		return err
	}
	if err := os.Setenv("PATH", mockBin+string(os.PathListSeparator)+oldPath); err != nil {
		return err
	}
	return tui.Run(version)
}

// CreateRepo seeds root with the demo git history and a .live-pr.toml matching
// the requested diff mode.
func CreateRepo(root, mode string) error {
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "live-pr demo"},
		{"config", "user.email", "demo@live-pr.invalid"},
	} {
		if err := demoGit(root, args...); err != nil {
			return err
		}
	}
	if err := writeDemoFile(root, "README.md", "# live-pr demo\n\nA disposable repository for reviewing the live-pr workflow.\n"); err != nil {
		return err
	}
	if err := writeDemoFile(root, "app.go", "package app\n\nfunc Greeting(name string) string {\n\treturn \"Hello, \" + name\n}\n"); err != nil {
		return err
	}
	if err := demoGit(root, "add", "."); err != nil {
		return err
	}
	if err := demoGit(root, "commit", "-m", "chore: seed demo repository"); err != nil {
		return err
	}
	if err := demoGit(root, "switch", "-c", "demo/"+mode); err != nil {
		return err
	}
	if err := writeDemoFile(root, "app.go", "package app\n\nimport \"fmt\"\n\nfunc Greeting(name string) string {\n\treturn fmt.Sprintf(\"Hello, %s!\", name)\n}\n"); err != nil {
		return err
	}
	if err := writeDemoFile(root, "app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestGreeting(t *testing.T) {\n\tif Greeting(\"demo\") != \"Hello, demo!\" {\n\t\tt.Fatal(\"unexpected greeting\")\n\t}\n}\n"); err != nil {
		return err
	}
	if err := demoGit(root, "add", "."); err != nil {
		return err
	}
	if err := demoGit(root, "commit", "-m", "feat: improve greeting and add coverage"); err != nil {
		return err
	}
	if err := writeDemoFile(root, "docs/review.md", "# Review notes\n\nThis file is intentionally added so the demo has a multi-file diff.\n"); err != nil {
		return err
	}
	if err := demoGit(root, "add", "."); err != nil {
		return err
	}
	if err := demoGit(root, "commit", "-m", "docs: add review notes"); err != nil {
		return err
	}

	config := `reviewer = ""

[diff]
command = ""
commit_command = ""
display = ""
`
	switch mode {
	case "delta":
		config = `reviewer = ""

[diff]
command = ""
commit_command = ""
display = "delta --color-only --paging=never --line-numbers"
`
	case "delta-side":
		config = `reviewer = ""

[diff]
command = ""
commit_command = ""
display = "delta --paging=never --side-by-side --line-numbers --width=\"$LIVE_PR_DIFF_WIDTH\""
`
	case "codereview":
		config = `[diff]
command = "nvim -c \"CodeDiff --inline $LIVE_PR_RANGE\""
commit_command = "nvim -c \"CodeDiff $LIVE_PR_SHA~1 $LIVE_PR_SHA\""
display = ""
`
	}
	if err := writeDemoFile(root, ".live-pr.toml", config); err != nil {
		return err
	}
	return writeDemoFile(root, ".git/info/exclude", ".live-pr.toml\n.demo-bin/\n.demo-github/\n.state/\n")
}

func restoreEnv(name, value string, existed bool) {
	if existed {
		_ = os.Setenv(name, value)
	} else {
		_ = os.Unsetenv(name)
	}
}

func writeDemoFile(root, name, content string) error {
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func demoGit(root string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, out)
	}
	return nil
}
