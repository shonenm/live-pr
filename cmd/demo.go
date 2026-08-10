package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/shonenm/live-pr/internal/tui"
	"github.com/spf13/cobra"
)

var demoCmd = &cobra.Command{
	Use:   "demo [git|delta|delta-side|codereview]",
	Short: "Open a disposable local review demo",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		mode := "git"
		if len(args) == 1 {
			mode = args[0]
		}
		switch mode {
		case "git", "delta", "delta-side", "codereview":
		default:
			return fmt.Errorf("unknown demo mode %q (use git, delta, delta-side, or codereview)", mode)
		}
		return runDemo(mode)
	},
}

func runDemo(mode string) error {
	root, err := os.MkdirTemp("", "live-pr-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	if err := createDemoRepo(root, mode); err != nil {
		return err
	}
	oldDir, err := os.Getwd()
	if err != nil {
		return err
	}
	defer os.Chdir(oldDir)
	if err := os.Chdir(root); err != nil {
		return err
	}
	oldState := os.Getenv("XDG_STATE_HOME")
	defer os.Setenv("XDG_STATE_HOME", oldState)
	if err := os.Setenv("XDG_STATE_HOME", filepath.Join(root, ".state")); err != nil {
		return err
	}
	return tui.Run()
}

func createDemoRepo(root, mode string) error {
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
display = "delta --color-only --paging=never --side-by-side --line-numbers"
`
	case "codereview":
		config = `[diff]
command = "nvim -c \"CodeDiff $LIVE_PR_BASE...$LIVE_PR_HEAD_REV\""
commit_command = "nvim -c \"CodeReview $LIVE_PR_SHA~1 $LIVE_PR_SHA\""
display = ""
`
	}
	return writeDemoFile(root, ".live-pr.toml", config)
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

func init() {
	rootCmd.AddCommand(demoCmd)
}
