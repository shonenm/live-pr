package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type demoPR struct {
	number int
	branch string
	oid    string
	title  string
	state  string
}

func setupDemoGitHub(root, mode string) (string, string, error) {
	current := "demo/" + mode
	origin := filepath.Join(root, ".git", "demo-origin.git")
	if err := demoGit(root, "init", "--bare", origin); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "remote", "add", "origin", origin); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "push", "-u", "origin", "main", current); err != nil {
		return "", "", err
	}
	currentOID, err := demoGitOutput(root, "rev-parse", current)
	if err != nil {
		return "", "", err
	}

	if err := demoGit(root, "switch", "main"); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "switch", "-c", "demo/remote"); err != nil {
		return "", "", err
	}
	if err := writeDemoFile(root, "remote.go", "package app\n\nfunc RemoteOnly() string { return \"review me\" }\n"); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "add", "remote.go"); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "commit", "-m", "feat: add remote-only change"); err != nil {
		return "", "", err
	}
	remoteOID, err := demoGitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	if err := demoGit(root, "push", "-u", "origin", "demo/remote"); err != nil {
		return "", "", err
	}

	if err := demoGit(root, "switch", "main"); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "switch", "-c", "demo/closed"); err != nil {
		return "", "", err
	}
	if err := writeDemoFile(root, "closed.md", "# Closed demo PR\n\nThis PR exists only in the local demo fixture.\n"); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "add", "closed.md"); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "commit", "-m", "docs: add closed PR fixture"); err != nil {
		return "", "", err
	}
	closedOID, err := demoGitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	if err := demoGit(root, "push", "-u", "origin", "demo/closed"); err != nil {
		return "", "", err
	}
	if err := demoGit(root, "switch", current); err != nil {
		return "", "", err
	}

	prs := []demoPR{
		{101, current, currentOID, "Improve greeting and add coverage", "OPEN"},
		{102, "demo/remote", remoteOID, "Add a remote-only change", "OPEN"},
		{99, "demo/closed", closedOID, "Document a completed change", "CLOSED"},
	}
	for _, pr := range prs {
		if err := demoGit(root, "--git-dir", origin, "update-ref", fmt.Sprintf("refs/pull/%d/head", pr.number), pr.oid); err != nil {
			return "", "", err
		}
	}
	if err := demoGit(root, "--git-dir", origin, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return "", "", err
	}

	stateDir := filepath.Join(root, ".demo-github")
	binDir := filepath.Join(root, ".demo-bin")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", "", err
	}
	for _, pr := range prs {
		if err := os.WriteFile(filepath.Join(stateDir, fmt.Sprintf("pr-%d", pr.number)), []byte(pr.state+"\n"), 0o644); err != nil {
			return "", "", err
		}
	}
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(demoGHScript(prs)), 0o755); err != nil {
		return "", "", err
	}
	return binDir, stateDir, nil
}

func demoGitOutput(root string, args ...string) (string, error) {
	cmd := append([]string{"-C", root}, args...)
	out, err := demoGitCommand(cmd...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func demoGHScript(prs []demoPR) string {
	body := "## Demo pull request\\n\\nAll GitHub data and actions in this screen are local mocks. User icons stay one terminal cell; Mermaid renders through termaid when installed.\\n\\n### Large Mermaid viewport test\\n\\n```mermaid\\ngraph TD\\n  A[GitHub pull request] --> B[Fetch metadata and conversation]\\n  B --> C{Content type}\\n  C -->|Markdown| D[Render with Glamour]\\n  C -->|Mermaid| E[Render with termaid]\\n  C -->|Avatar| F[Download from GitHub]\\n  E --> G[Compact to pane width]\\n  F --> H[Reduce to representative color]\\n  D --> I[Conversation card]\\n  G --> I\\n  H --> J[One-cell user icon]\\n  J --> I\\n  I --> K[Scrollable viewport]\\n  K --> L[Terminal over SSH]\\n  K --> M[Terminal inside tmux]\\n  K --> N[Local terminal]\\n```"
	body = strings.ReplaceAll(body, "`", "\\`")
	var cases strings.Builder
	for _, pr := range prs {
		fmt.Fprintf(&cases, `
    %d) branch=%q; oid=%q; title=%q ;;
`, pr.number, pr.branch, pr.oid, pr.title)
	}
	return fmt.Sprintf(`#!/bin/sh
set -eu
state_dir=${LIVE_PR_DEMO_STATE:?}
number=${3:-0}

pr_data() {
  case "$1" in%s
    *) exit 1 ;;
  esac
  state=$(tr -d '\n' < "$state_dir/pr-$1")
}

commit_status_json() {
  commit_oids=$(git rev-list --reverse main.."$1")
  count=$(printf '%%s\n' "$commit_oids" | wc -w | tr -d ' ')
  index=0
  items=
  for commit_oid in $commit_oids; do
    check_state=SUCCESS
    if [ "$count" -gt 1 ] && [ "$index" -eq 0 ]; then check_state=FAILURE; fi
    headline=$(git log -1 --format=%%s "$commit_oid")
    committed=$(git log -1 --format=%%cI "$commit_oid")
    [ -z "$items" ] || items="$items,"
    items="$items{\"commit\":{\"oid\":\"$commit_oid\",\"committedDate\":\"$committed\",\"messageHeadline\":\"$headline\",\"statusCheckRollup\":{\"state\":\"$check_state\"}}}"
    index=$((index + 1))
  done
  printf '%%s' "$items"
}

pr_json() {
  pr_data "$1"
  cat <<JSON
{"number":$1,"url":"https://example.invalid/pull/$1","title":"$title","body":"%s","state":"$state","baseRefName":"main","headRefName":"$branch","headRefOid":"$oid","isDraft":false,"isCrossRepository":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED","additions":24,"deletions":6,"changedFiles":3,"updatedAt":"2026-08-10T12:00:00Z","createdAt":"2026-08-10T10:00:00Z","author":{"login":"demo-user","avatarUrl":"https://avatars.githubusercontent.com/shonenm"},"assignees":[{"login":"demo-user"}],"labels":[{"name":"demo","color":"1f6feb"}],"reviewRequests":[],"comments":[{"author":{"login":"reviewer"},"body":"This is mock review feedback.","createdAt":"2026-08-10T11:00:00Z","url":"https://example.invalid/pull/$1#comment"}],"statusCheckRollup":[{"name":"demo-check","status":"COMPLETED","conclusion":"SUCCESS"}]}
JSON
}

list_json() {
  pr_data "$1"
  cat <<JSON
{"number":$1,"url":"https://example.invalid/pull/$1","title":"$title","state":"$state","baseRefName":"main","headRefName":"$branch","headRefOid":"$oid","isDraft":false,"isCrossRepository":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED","updatedAt":"2026-08-10T12:00:00Z","createdAt":"2026-08-10T10:00:00Z","author":{"login":"demo-user","avatarUrl":"https://avatars.githubusercontent.com/shonenm"},"assignees":{"nodes":[{"login":"demo-user"}]},"labels":{"nodes":[{"name":"demo","color":"1f6feb"}]},"reviewRequests":{"nodes":[]},"statusCheckRollup":{"state":"SUCCESS"}}
JSON
}

sleep 0.45
case "${1:-} ${2:-}" in
  "repo view")
    printf '%%s\n' '{"nameWithOwner":"demo/live-pr"}'
    ;;
  "api graphql")
    wanted=OPEN
    commit_number=
    for arg in "$@"; do
      [ "$arg" = "state=CLOSED" ] && wanted=CLOSED
      case "$arg" in
        number=*) commit_number=${arg#number=} ;;
        searchQuery=*) printf '%%s' "${arg#searchQuery=}" | grep -q 'is:closed' && wanted=CLOSED ;;
      esac
    done
    if [ -n "$commit_number" ]; then
      pr_data "$commit_number"
      nodes=$(commit_status_json "$oid")
      printf '{"data":{"repository":{"pullRequest":{"commits":{"nodes":[%%s],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}\n' "$nodes"
      exit 0
    fi
    nodes=
    for n in 101 102 99; do
      pr_data "$n"
      [ "$state" = "$wanted" ] || continue
      item=$(list_json "$n")
      [ -z "$nodes" ] || nodes="$nodes,"
      nodes="$nodes$item"
    done
    count=0
    [ -z "$nodes" ] || count=$(printf '%%s' "$nodes" | awk -F'"number":' '{print NF-1}')
    printf '{"data":{"viewer":{"login":"demo-user"},"search":{"issueCount":%%s,"nodes":[%%s],"pageInfo":{"hasNextPage":false}}}}\n' "$count" "$nodes"
    ;;
  "pr list")
    head=
    wanted=OPEN
    while [ "$#" -gt 0 ]; do
      [ "$1" = "--head" ] && { head=$2; shift; }
      [ "$1" = "--state" ] && { wanted=$(printf '%%s' "$2" | tr '[:lower:]' '[:upper:]'); shift; }
      shift
    done
    found=
    for n in 101 102 99; do
      pr_data "$n"
      [ "$branch" = "$head" ] || continue
      [ "$wanted" = ALL ] || [ "$state" = "$wanted" ] || continue
      found=$(pr_json "$n")
    done
    [ -n "$found" ] && printf '[%%s]\n' "$found" || printf '[]\n'
    ;;
  "pr view")
    pr_json "$number"
    ;;
  "pr merge")
    printf 'CLOSED\n' > "$state_dir/pr-$number"
    ;;
  "pr close")
    printf 'CLOSED\n' > "$state_dir/pr-$number"
    ;;
  "pr checkout")
    pr_data "$number"
    git switch "$branch" >/dev/null
    ;;
  "api --paginate")
    endpoint=${4:-}
    case "$endpoint" in
      */comments*) printf '[[{"id":1,"body":"Mock conversation comment","created_at":"2026-08-10T11:00:00Z","updated_at":"2026-08-10T11:00:00Z","html_url":"https://example.invalid/comment/1","user":{"login":"reviewer","avatar_url":"https://avatars.githubusercontent.com/github"}}]]\n' ;;
      *) printf '[[{"id":2,"event":"labeled","created_at":"2026-08-10T10:30:00Z","actor":{"login":"demo-user","avatar_url":"https://avatars.githubusercontent.com/shonenm"},"label":{"name":"demo"}}]]\n' ;;
    esac
    ;;
  *)
    printf 'unsupported demo gh command: %%s\n' "$*" >&2
    exit 1
    ;;
esac
`, cases.String(), body)
}

func demoGitCommand(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
