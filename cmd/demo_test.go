package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
)

func TestCreateDemoRepo(t *testing.T) {
	root := t.TempDir()
	if err := createDemoRepo(root, "delta"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "log", "--oneline", "main..HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(out)), "\n")); got != 2 {
		t.Fatalf("demo commits = %d, want 2", got)
	}
	config, err := os.ReadFile(root + "/.live-pr.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `display = "delta --color-only --paging=never --line-numbers"`) {
		t.Fatalf("demo config = %s", config)
	}
}

func TestSetupDemoGitHubProvidesStatefulActions(t *testing.T) {
	root := t.TempDir()
	if err := createDemoRepo(root, "git"); err != nil {
		t.Fatal(err)
	}
	binDir, stateDir, err := setupDemoGitHub(root, "git")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(binDir, "gh")).CombinedOutput(); err != nil {
		t.Fatalf("mock gh syntax: %v: %s", err, out)
	}
	t.Setenv("LIVE_PR_DEMO_STATE", stateDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	list, err := gh.New().SearchPRs("is:open", "")
	if err != nil || list.TotalCount != 2 || len(list.PRs) != 2 || list.PRs[0].Number != 101 || list.PRs[1].Number != 102 {
		t.Fatalf("GitHub client open page = %#v err=%v", list, err)
	}
	preview, err := gh.New().FindPreview(102)
	if err != nil || preview.Number != 102 || !preview.PreviewLoaded || len(preview.Conversation) != 1 || len(preview.Commits) != 1 || preview.Commits[0].CheckRollupState != "SUCCESS" {
		t.Fatalf("GitHub client preview = %#v err=%v", preview, err)
	}
	currentPreview, err := gh.New().FindPreview(101)
	if err != nil || len(currentPreview.Commits) != 2 || currentPreview.Commits[0].CheckRollupState != "FAILURE" || currentPreview.Commits[0].MessageHeadline == "" || currentPreview.Commits[0].CommittedDate == "" || currentPreview.Commits[1].CheckRollupState != "SUCCESS" {
		t.Fatalf("GitHub client mixed commit CI preview = %#v err=%v", currentPreview, err)
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("gh", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gh %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	open := run("api", "graphql", "-F", "state=OPEN")
	if !strings.Contains(open, `"number":101`) || !strings.Contains(open, `"number":102`) || strings.Contains(open, `"number":99`) {
		t.Fatalf("open PR fixture = %s", open)
	}
	closed := run("api", "graphql", "-F", "state=CLOSED")
	if !strings.Contains(closed, `"number":99`) || strings.Contains(closed, `"number":101`) {
		t.Fatalf("closed PR fixture = %s", closed)
	}
	run("pr", "merge", "101", "--merge", "--match-head-commit", "ignored")
	if state, err := os.ReadFile(filepath.Join(stateDir, "pr-101")); err != nil || strings.TrimSpace(string(state)) != "CLOSED" {
		t.Fatalf("merge state = %q err=%v", state, err)
	}
	merged, err := gh.New().FindForHead("demo/git")
	if err != nil || merged.Number != 101 || merged.State != "CLOSED" {
		t.Fatalf("merged branch lookup = %#v err=%v", merged, err)
	}
	run("pr", "close", "102")
	if state, err := os.ReadFile(filepath.Join(stateDir, "pr-102")); err != nil || strings.TrimSpace(string(state)) != "CLOSED" {
		t.Fatalf("close state = %q err=%v", state, err)
	}
	run("pr", "checkout", "102")
	branch := exec.Command("git", "-C", root, "branch", "--show-current")
	out, err := branch.Output()
	if err != nil || strings.TrimSpace(string(out)) != "demo/remote" {
		t.Fatalf("checkout branch = %q err=%v", out, err)
	}
}
