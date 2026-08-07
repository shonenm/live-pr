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

Equivalent behavior is built in; use the same keys in the global or per-repository config to override it:

```toml
[diff]
command = 'nvim -c "CodeReviewBranch $LIVE_PR_BASE"'
commit_command = 'nvim -c "CodeReview $LIVE_PR_SHA~1 $LIVE_PR_SHA"'
```

The command runs in a real embedded pseudoterminal (PTY), so full-screen tools such as Neovim work without suspending live-pr or opening another tmux pane. `command = ""` explicitly disables the built-in branch reviewer. The built-in commit behavior is deliberately supplied through the legacy top-level `reviewer` fallback, so an explicit `commit_command` takes precedence and existing `reviewer` overrides remain compatible.

It starts in the repository root with:

- `LIVE_PR_BASE`: PR base branch;
- `LIVE_PR_HEAD`: current branch;
- `LIVE_PR_PR_URL`: cached GitHub PR URL, or empty before a PR exists;
- `LIVE_PR_SHA`: selected commit in commit review, otherwise empty;
- `TERM=xterm-256color` from the embedded terminal.

`CodeReviewBranch` compares the merge base with `HEAD`, matching the branch-only three-dot range used for a PR. live-pr takes the base from the cached/fetched GitHub PR (`baseRefName`); `live-pr pr --base …` also persists it. For local comparison it prefers the matching `origin/<base>` remote-tracking ref over a possibly stale local branch. The repository default branch is used only before a PR-specific base is known.

## Interaction

The default screen has no tabs: Conversation is always on the left and branch-wide Files changed / CodeReview is always on the right.

- `c` while the left pane is focused replaces Conversation with the commit picker;
- `j`/`k` selects a commit;
- `Enter` restarts the right pane with `commit_command` and `LIVE_PR_SHA`, then focuses it;
- `l` focuses the right review pane; `q` returns to the left from review, and exits live-pr when already on the left;
- `Shift+Tab` remains the shared focus toggle;
- `Esc` from the commit picker restores Conversation and branch-wide `command` review;
- clicking either pane also changes focus;
- while CodeReview has focus, keys other than reserved `q` and `Shift+Tab` are sent directly to Neovim;
- the active CodeReview border uses the accent color.

The PTY is resized with the right pane. Each branch/commit switch closes and reaps the previous process, and generation-specific IDs discard its late messages.

## Fallback display

When the command for the current scope is disabled, unsupported, exits, or fails, live-pr keeps a matching built-in view:

```text
branch scope → git diff base...HEAD
commit scope → git show SHA
```

An optional non-interactive formatter remains available as a fallback:

```toml
[diff]
display = "delta --color-only"
```

`display` receives raw Git diff on stdin and writes terminal text to stdout. The active embedded command owns the pane while running; `display` is used only when that command is absent, exits, or fails.

Static formatter behavior remains:

- raw Git appears first;
- formatting runs in the background;
- stale results are discarded;
- results are cached by command, width, and diff identity;
- failure or timeout retains raw Git.

## Architecture

- `internal/git`: raw file/commit fallback diffs;
- `internal/config`: branch `command`, `commit_command`, and static `display`;
- `internal/embeddedterm`: PTY/VT lifecycle around Portalis;
- `internal/diffview`: bounded non-interactive fallback formatter;
- `internal/tui`: fixed two-pane layout, commit picker, scope/focus routing, and fallback selection.

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
