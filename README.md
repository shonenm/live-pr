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

## Status

Go implementation in progress (Bubble Tea + Lipgloss, single binary). See
[docs/roadmap.md](docs/roadmap.md).

- **P0–P2 (done)** — event model + JSONL store, CLI (`append`/`note`/`decision`/`pivot`/`init`), TUI, and pluggable reviewer launch on a commit.
- **P3 (done)** — auto-feed `base..HEAD` commits into the timeline (`live-pr sync`, and on TUI open).
- **P4 (done)** — Claude Code `Stop` hook (`live-pr hook stop`) summarizes each session into the timeline, throttled.
- **P5 (done)** — create or update a GitHub PR from the conclusion and timeline (`live-pr pr`). Only the marked live-pr section is updated, so other PR body content is preserved; conflicting managed edits fail safely.
- **Current TUI** — functional Conversation / Files changed / Commits tabs beside an optional PTY-embedded CodeReview workspace. `[diff].command` opens the interactive reviewer at startup against the PR-equivalent branch range; raw Git and `[diff].display` remain fallbacks. Cached PR metadata, assignees, labels, top-level comments, and GitHub activity are shown immediately; the header exposes assignees and color-matched label pills. GitHub state refreshes once on open and again only when you press `r`.
- **PR workflow** — press `p` to explicitly create/update the PR. Local events and GitHub comments use bordered cards with different border intensity instead of source labels; GitHub activity and Git commits remain unboxed timeline rows. Image/video embeds stay as URLs, and `o` opens the focused GitHub comment.
- **Next** — add outbound comments and review/inline-comment sync, then move to distribution, theming, and other-agent adapters.

```sh
go build -o live-pr .

live-pr init            # create .live-pr/<branch>/ for this branch
live-pr init --hooks    # print the Claude Code Stop-hook config to install
live-pr sync            # import base..HEAD commits
live-pr                 # open the three-tab TUI (Tab/Shift+Tab or 1/2/3)
                        # r: refresh GitHub; p: publish PR; o: open comment
                        # Shift+Tab: focus local PR / embedded CodeReview
live-pr pr --dry-run    # preview the generated managed PR body
live-pr pr              # push and create or update the GitHub PR
```

Configure the right-side local PR review workspace in `~/.config/live-pr/config.toml` (or per-repo `.live-pr/config.toml`):

```toml
[diff]
command = 'nvim -c "CodeReviewBranch $LIVE_PR_BASE"'
display = "delta --color-only" # fallback after CodeReview exits
```

Without `command`, the right pane uses raw Git output, optionally filtered by `display`. The legacy commit-scoped `reviewer` setting remains available when embedded CodeReview is disabled.

The original fzf experience mock lives in `prototype/`. Right-pane display configuration and its boundary with the existing external reviewer are documented in [docs/diff-tool-integration.md](docs/diff-tool-integration.md).
