# Embedded local PR code review

## Product goal

live-pr has two core surfaces that must remain visible together:

```text
┌ local PR ─────────────────┬ CodeReview ─────────────────────┐
│ conversation and history │ interactive base...HEAD review │
└───────────────────────────┴─────────────────────────────────┘
```

The right pane is not merely a prettier `git diff`. It is an interactive local code-review workspace for the same change range represented by the GitHub PR. The reviewer opens when live-pr starts and stays inside the Bubble Tea screen.

## Configuration

```toml
[diff]
command = 'nvim -c "CodeReviewBranch $LIVE_PR_BASE"'
```

The command runs in a real embedded pseudoterminal (PTY), so full-screen tools such as Neovim work without suspending live-pr or opening another tmux pane.

It starts in the repository root with:

- `LIVE_PR_BASE`: PR base branch;
- `LIVE_PR_HEAD`: current branch;
- `LIVE_PR_PR_URL`: cached GitHub PR URL, or empty before a PR exists;
- `TERM=xterm-256color` from the embedded terminal.

`CodeReviewBranch` compares the merge base with `HEAD`, matching the branch-only three-dot range used for a PR. live-pr takes the base from the cached/fetched GitHub PR (`baseRefName`); `live-pr pr --base …` also persists it. For local comparison it prefers the matching `origin/<base>` remote-tracking ref over a possibly stale local branch. The repository default branch is used only before a PR-specific base is known.

## Interaction

- local PR has focus at startup;
- `Shift+Tab` switches focus between local PR and CodeReview;
- clicking either pane also changes focus;
- while CodeReview has focus, keys are sent directly to Neovim;
- `Shift+Tab` is reserved by live-pr and is not sent to Neovim;
- the active CodeReview border uses the accent color;
- switch back to local PR before pressing `q` to exit live-pr.

The PTY is resized with the right pane, preserving Neovim's full-screen behavior. Closing live-pr closes and reaps the embedded reviewer process.

## Fallback display

Without `command`, live-pr keeps its built-in selected-file/commit Git output:

```text
Git diff/show → right pane
```

An optional non-interactive formatter remains available as a fallback:

```toml
[diff]
display = "delta --color-only"
```

`display` receives raw Git diff on stdin and writes terminal text to stdout. If both are configured, `command` owns the pane while it is running; `display` is used only after the embedded reviewer exits or fails.

Static formatter behavior remains:

- raw Git appears first;
- formatting runs in the background;
- stale results are discarded;
- results are cached by command, width, and diff identity;
- failure or timeout retains raw Git.

## Existing commit reviewer

The legacy commit-scoped setting remains for configurations without an embedded reviewer:

```toml
reviewer = 'nvim -c "CodeReview {sha}~1 {sha}"'
```

When no embedded `diff.command` is active, Enter launches it using the old suspend/resume path. It is disabled while embedded CodeReview owns the right pane.

## Architecture

- `internal/git`: raw file/commit fallback diffs;
- `internal/config`: `[diff].command` and `[diff].display`;
- `internal/embeddedterm`: PTY/VT lifecycle around Portalis;
- `internal/diffview`: bounded non-interactive fallback formatter;
- `internal/tui`: layout, focus, event routing, and fallback selection;
- `internal/review`: legacy commit-scoped external launch.

The embedded terminal supplies PTY process I/O, xterm key encoding, ANSI/VT parsing, alternate-screen support, Unicode cell widths, mouse events, and resize propagation. live-pr remains responsible for pane allocation and which events are forwarded. Portalis is pinned to a commit through a temporary module-path replacement until its repository/module names align.

## Failure behavior

If the embedded command cannot start or exits unexpectedly:

1. return focus to local PR;
2. show the failure in the footer;
3. restore the configured static formatter or raw Git fallback;
4. keep GitHub refresh, publish, and conversation navigation operational.

The embedded PTY is currently supported on macOS and Linux. Windows builds retain the raw/static diff fallback because the pinned PTY implementation is Unix-only.

## Non-goals

- implementing a second code-review UI inside live-pr;
- parsing Neovim's screen back into structured diff objects;
- syncing CodeReview's in-memory reviewed marks to GitHub;
- requiring tmux or another external pane manager;
- making GitHub network access a prerequisite for local review.
