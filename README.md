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

- **head** — the current conclusion, pinned on top (ignore the messy flow, see
  where we are now)
- **timeline** — an append-only, chronological feed of `decision` / `pivot` /
  `summary` / `commit` / `note` events, fed automatically by agent hooks
- **reviewer** — pluggable; an interactive local reviewer such as Neovim CodeReview can stay embedded beside the conversation and review the full PR-equivalent branch diff
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
| `octorus` | terminal PR + local diff review, AI loop | no decision timeline |
| `gh-dash` | GitHub UI in the terminal | dashboard, not a living PR |
| Copilot / PR-describe | PR description generation | compresses the final diff |

## Features

- Append-only decision timeline for `decision`, `pivot`, `summary`, `commit`, and `note` events.
- Bubble Tea TUI with GitHub-style open-PR lists, filters, saved navigation state, stack grouping, and cached previews.
- Local and remote PR review without changing the worktree for remote browsing.
- Conversation beside an interactive CodeReview pane, with commit-scoped review support.
- Static `git diff`/`delta` review with a file Explorer, immediate file selection, reviewed checks, and independent Conversation/Diff scrolling.
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

### Requirements and current scope

- Git is required for local use.
- An authenticated GitHub CLI (`gh`) is required only for GitHub browsing and publishing.
- Claude Code is required only for automatic Stop-hook summaries.
- Neovim CodeReview and external diff formatters are optional; raw Git diff is built in.
- macOS and Linux support the embedded reviewer. Windows falls back to static diff.
- GitHub review comments and inline comments are not synchronized yet.
- Local state remains outside the repository until an explicit `live-pr pr` publish.

Runtime state is stored outside the repository under the XDG state directory (`~/.local/state/live-pr` on Linux). Existing `.live-pr/` data is migrated automatically; repo-specific configuration uses `.live-pr.toml`.

## Development

```sh
just check              # tests, targeted race checks, vet, module verification, and working-tree diff check
go build -o live-pr .

live-pr                 # auto-detect the repository and current branch
live-pr demo             # disposable demo with built-in git diff
live-pr demo delta       # disposable demo with delta (unified)
live-pr demo delta-side  # disposable demo with delta (side-by-side)
live-pr demo codereview  # disposable demo with embedded Neovim CodeReview
                         # every demo includes stateful mock PRs; no GitHub resources are created
live-pr init            # optional: seed the current branch's local conclusion
live-pr init --hooks    # print the Claude Code Stop-hook config to install
live-pr sync            # import base..HEAD commits
                        # list: [/] Assigned/Review/All/Closed views; / filter (is:closed); Space stack; j/k select; Enter open; c checkout; x close; m merge; r refresh; q quit
                        # detail: b list; c commits; l review/Explorer; Ctrl+U/D scroll; m merge; q left/quit
live-pr pr --dry-run    # preview the generated managed PR body
live-pr pr              # push and create or update the GitHub PR
```

Set `LIVE_PR_DEBUG_TIMING=1` to print opt-in startup, Git, GitHub, cache-save, and TUI synchronization timings to stderr.

In a demo, press `b` for the mocked PR list, then use `m`, `c`, and `x` to exercise merge, checkout, and close. Select the Closed view or search `is:closed` to verify that completed mock PRs move out of Open. The checkout changes only the disposable repository; all GitHub data stays local.

The built-in right-side reviewer starts Neovim with an explicit three-dot `CodeDiff` range for local or remote PRs and `CodeReview` for a selected commit. Override it in `~/.config/live-pr/config.toml` (or per-repo `.live-pr.toml`):

```toml
[diff]
command = 'nvim -c "CodeDiff $LIVE_PR_BASE...$LIVE_PR_HEAD_REV"'
commit_command = 'nvim -c "CodeReview $LIVE_PR_SHA~1 $LIVE_PR_SHA"'
display = "delta --color-only" # fallback after CodeReview exits
```

Set `command = ""` to disable the built-in branch reviewer. When the command for a scope is empty, unsupported, exits, or fails, the right pane uses the whole raw branch diff or selected commit diff, optionally filtered by `display`.

The original fzf experience mock lives in `prototype/`. Right-pane display configuration and its boundary with the existing external reviewer are documented in [docs/diff-tool-integration.md](docs/diff-tool-integration.md).
