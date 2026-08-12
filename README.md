```
╻  ╻╻ ╻┏━╸   ┏━┓┏━┓
┃  ┃┃┏┛┣╸ ╺━╸┣━┛┣┳┛
┗━╸╹┗┛ ┗━╸   ╹  ╹┗╸
```

# live-pr

Living pull request for LLM-assisted development.

Capture the **decision / iteration timeline** of an AI coding session — not just
the final diff — in a local, GitHub-PR-style TUI, and export it to a real pull
request that reflects that timeline.

## Why

A GitHub PR records only the compressed *final* conclusion. The actual
iteration — pivots, discarded approaches, why each decision was made — happens
locally with the coding agent and is thrown away. The decision flow never lands
on the timeline; the PR is written once, at the end, as a terse summary.

live-pr keeps a living artifact during development:

- **head** — the final PR-template summary, pinned on top and updated when the implemented outcome is known
- **timeline** — an append-only, editable view of sparse `decision` / `pivot` /
  `summary` / `commit` / `note` records from humans and agents
- **reviewer** — pluggable; an interactive local reviewer such as Neovim CodeDiff can stay embedded beside the conversation and review the full PR-equivalent branch diff
- **export** — at the end, the timeline is turned into a real GitHub PR body

Read it in a tmux popup, review code like a GitHub PR, ship it as a PR whose
description carries the decision flow.

## Prior art

Deep research (2026-07) into the landscape: the specific combination —
agent-fed decision timeline + local PR-style TUI + timeline-reflecting PR
export — is a genuine gap. No single tool does all three. Closest pieces:

| Tool | Covers | Missing |
| --- | --- | --- |
| `hex/claude-sessions` | `timeline.jsonl` + labelled checkpoints | manual, no PR export |
| Agent Decision Records | decisions surfaced as PR links | snapshot-based, no TUI |
| `Hunk` | agent-controllable diff review and inline comments | review session, not a PR-level decision timeline |
| `git-appraise` | distributed local reviews and comments in Git objects | no living summary or GitHub PR body workflow |
| `octorus` | terminal PR + local diff review, AI loop | no decision timeline |
| `gh-dash` | GitHub UI in the terminal | dashboard, not a living PR |
| Copilot / PR-describe | PR description generation | compresses the final diff |

## Features

- Append-only decision history with stable IDs and non-destructive add/edit/delete operations from both the TUI and CLI.
- Final summary seeded from the repository's default GitHub pull-request template and rendered above the timeline.
- Version-matched Agent Skill teaching coding agents when to record decisions—and when not to.
- Bubble Tea TUI with GitHub-style open-PR lists, filters, saved navigation state, stack grouping, and cached previews.
- Local and remote PR review without changing the worktree for remote browsing.
- Conversation beside an interactive CodeDiff pane, with detail-header CI/review/size status, local uncommitted-change visibility, and commit-scoped CI/review support. Pending CI refreshes every 15 seconds while that PR detail remains open, then stops on completion.
- Static `git diff`/`delta` review with a file Explorer, immediate file selection, reviewed checks, conflict-file markers, and independent Conversation/Diff scrolling.
- Create or update GitHub PRs from the conclusion and timeline with `live-pr pr`.
- Explicit checkout, close, and merge actions with centered confirmation popups and loading indicators.
- Claude Code Stop-hook integration and XDG-compliant runtime state.

See [docs/diff-tool-integration.md](docs/diff-tool-integration.md) for reviewer configuration and pane controls.

## Install

Download a platform archive from [GitHub Releases](https://github.com/shonenm/live-pr/releases). For example, Linux amd64:

```sh
version=v0.1.0
asset=live-pr_0.1.0_Linux_amd64.tar.gz
tmp=$(mktemp -d)
curl -fL "https://github.com/shonenm/live-pr/releases/download/$version/$asset" -o "$tmp/$asset"
tar -xzf "$tmp/$asset" -C "$tmp"
install -Dm755 "$tmp/live-pr" "$HOME/.local/bin/live-pr"
export PATH="$HOME/.local/bin:$PATH"
live-pr --version
live-pr demo
```

Homebrew packages are not currently provided. Go users can install the latest source version with:

```sh
go install github.com/shonenm/live-pr@latest
```

### Agent Skill

Install the repository's [Agent Skills Standard](https://agentskills.io/) skill with a recent GitHub CLI:

```sh
gh skill install shonenm/live-pr live-pr --agent pi --scope user
# replace pi with claude-code, codex, or another supported host
```

The binary also carries the matching skill version. `live-pr skill path` materializes it and prints its path; `live-pr skill print` writes it to stdout.

### Requirements and current scope

- Git is required for local use.
- An authenticated GitHub CLI (`gh`) is required only for GitHub browsing and publishing.
- Claude Code is required only for automatic Stop-hook summaries.
- Neovim CodeDiff and external diff formatters are optional; raw Git diff is built in.
- macOS and Linux support the embedded reviewer. Windows falls back to static diff.
- GitHub review submission supports general and inline comments, approval, and changes requested. Existing review threads and inline comments are not synchronized into the TUI yet.
- Local state remains outside the repository until an explicit `live-pr pr` publish.

Runtime state is stored outside the repository under the XDG state directory (`~/.local/state/live-pr` on Linux). Existing `.live-pr/` data is migrated automatically; repo-specific configuration uses `.live-pr.toml`.

## Development

```sh
just check              # tests, targeted race checks, vet, module verification, and working-tree diff check
go build -o live-pr .

live-pr                  # open the TUI for the current repository and branch
live-pr -C ../repo        # run any command against another working directory
live-pr status            # show the cache-first local/GitHub PR status
live-pr status --json     # stable machine-readable status; add --refresh to query GitHub
live-pr demo              # disposable demo with built-in git diff
live-pr demo delta       # disposable demo with delta (unified)
live-pr demo delta-side  # disposable demo with delta (side-by-side)
live-pr demo codereview  # disposable demo with embedded Neovim CodeDiff
                         # every demo includes stateful mock PRs; no GitHub resources are created
live-pr init             # seed the final summary from the repo PR template
live-pr summary edit     # edit the final result in $VISUAL/$EDITOR
live-pr summary set --file summary.md
live-pr comment add "Use GraphQL" --kind decision --body "Avoids repeated requests"
live-pr comment list --json
live-pr comment edit <id> "Use batched GraphQL" --body "One request"
live-pr comment delete <id>
live-pr review body --body "General review feedback"
live-pr review add internal/app.go --line 42 --side RIGHT --body "Handle this error."
live-pr review show --json
live-pr review submit --event REQUEST_CHANGES # or COMMENT / APPROVE
live-pr init --hooks     # print the optional, significance-filtered Stop hook
live-pr sync             # import base..HEAD commits
                        # list: [/] Assigned/Review/All/Closed views; / filter + Enter; Space stack; j/k select; Enter open; c checkout; x close; m merge; r refresh; q quit
                        # detail: Local PR: a/e/d local summary/comment; GitHub PR: a general review body
                        #         Explorer: a/A inline review comment; v inspect/submit Comment/Approve/Request changes
                        #         Ctrl+S save; Esc cancel; b list; c commits; l review/Explorer; Ctrl+U/D scroll; m merge; q left/quit
live-pr pr preview       # preview the generated managed PR body
live-pr pr publish       # push and create or update the GitHub PR
                         # compatible aliases: live-pr pr --dry-run / live-pr pr
```

Conversation uses portable Unicode user markers, avoiding terminal-specific image protocols and avatar downloads. Mermaid fences render as labeled, syntax-highlighted source when no graphical renderer is available; live-pr does not require Kitty/Sixel support, Node.js, Chromium, or `mmdc`.

Remote PR detail shows commits behind the latest fetched base and conflict files from a non-mutating `git merge-tree` simulation. Neither check changes HEAD, the index, or the worktree.

Set `LIVE_PR_DEBUG_TIMING=1` to print opt-in startup, Git, GitHub, cache-save, and TUI synchronization timings to stderr.

The PR navigator fetches only the active view's first 25 rows. Reaching the final loaded row requests the next page and appends it; switching back to a view loaded in the current session does not issue another request. View counts become exact when that view is first fetched (`?` means unvisited). Search is submitted with `Enter` and runs server-side; `ci:` and `merge:` remain local post-filters over progressively loaded pages. GitHub Search limits any one query to its first 1,000 results.

In a demo, the current PR Conversation shows compact CI activity for two commits: the first has a mocked red failure and the second a green success. Press `c` to inspect the same results in the full two-commit list. Press `b` for the mocked PR list, then use `m`, `c`, and `x` to exercise merge, checkout, and close. Select the Closed view or search `is:closed` to verify that completed mock PRs move out of Open. The checkout changes only the disposable repository; all GitHub data stays local.

Review drafts live under repository XDG state, isolated by PR number and head commit. Submission rechecks the current GitHub head SHA and refuses stale line comments after a push. Press `A` from the left pane to comment on the currently selected changed file. With the static Explorer (`[diff].command = ""`), `a` on a selected file does the same to enter `path`, `line`, `side` (`RIGHT` for new code, `LEFT` for deleted code), and the body. In Conversation, `a` edits the general review body. Press `v` to inspect and submit the draft. External Neovim reviewers remain usable, but live-pr cannot infer their cursor line; use `live-pr review add` for explicit inline comments.

The built-in right-side reviewer starts Neovim with `LIVE_PR_RANGE`: a merge-base-to-working-tree comparison for checked-out Local PRs, or the historical PR base-to-fetched-head three-dot range for remote PRs. Selected commits use `CodeDiff` with the commit's parent-to-commit range. Override it in `~/.config/live-pr/config.toml` (or per-repo `.live-pr.toml`):

```toml
[diff]
command = 'nvim -c "CodeDiff $LIVE_PR_RANGE"'
commit_command = 'nvim -c "CodeDiff $LIVE_PR_SHA~1 $LIVE_PR_SHA"'
display = "delta --color-only" # fallback after CodeDiff exits
```

Set `command = ""` to disable the built-in branch reviewer. When the command for a scope is empty, unsupported, exits, or fails, the right pane uses the whole raw branch diff or selected commit diff, optionally filtered by `display`.

The original fzf experience mock lives in `prototype/`. Right-pane display configuration and its boundary with the existing external reviewer are documented in [docs/diff-tool-integration.md](docs/diff-tool-integration.md).
