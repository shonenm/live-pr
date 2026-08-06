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
- **reviewer** — pluggable; the diff review is delegated to your own tool
  (nvim / hunk viewer / difit / …), launched scoped to a commit
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
- **Current TUI** — functional Conversation / Files changed / Commits tabs with local diffs. Cached PR metadata is shown immediately, refreshed once on open, and refreshed again only when you press `r`.
- **Next** — complete GitHub conversation sync, then move to distribution, theming, and other-agent adapters.

```sh
go build -o live-pr .

live-pr init            # create .live-pr/<branch>/ for this branch
live-pr init --hooks    # print the Claude Code Stop-hook config to install
live-pr sync            # import base..HEAD commits
live-pr                 # open the three-tab TUI (Tab/Shift+Tab or 1/2/3)
                        # r: refresh GitHub metadata; Enter: review a commit
live-pr pr --dry-run    # preview the generated managed PR body
live-pr pr              # push and create or update the GitHub PR
```

Reviewer defaults to `nvim -c "CodeDiff {sha}~1 {sha}"`; override in
`~/.config/live-pr/config.toml` (or per-repo `.live-pr/config.toml`).

The original fzf experience mock lives in `prototype/`.
