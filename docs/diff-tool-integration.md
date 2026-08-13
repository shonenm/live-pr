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
command = 'nvim -c "CodeDiff --inline $LIVE_PR_BASE...$LIVE_PR_HEAD_REV"'
commit_command = 'nvim -c "CodeDiff $LIVE_PR_SHA~1 $LIVE_PR_SHA"'
```

The command runs in a real embedded pseudoterminal (PTY), so full-screen tools such as Neovim work without suspending live-pr or opening another tmux pane. `command = ""` explicitly disables the built-in branch reviewer. The built-in commit behavior is deliberately supplied through the legacy top-level `reviewer` fallback, so an explicit `commit_command` takes precedence and existing `reviewer` overrides remain compatible.

It starts in the repository root with:

- `LIVE_PR_BASE`: PR base branch;
- `LIVE_PR_HEAD`: logical local or remote head branch;
- `LIVE_PR_HEAD_REV`: explicit review revision (`HEAD` locally or a namespaced fetched PR ref);
- `LIVE_PR_PR_URL`: cached GitHub PR URL, or empty before a PR exists;
- `LIVE_PR_SHA`: selected commit in commit review, otherwise empty;
- `TERM=xterm-256color` from the embedded terminal.

The explicit `base...head` range matches GitHub PR semantics for both the checkout and browsed PRs. live-pr takes the base from GitHub, prefers `origin/<base>`, and uses `HEAD` only for local detail. For another PR it fetches numeric `refs/pull/<N>/head` into `refs/live-pr/pulls/<N>/head`, verifies the advertised OID, and never checks out or resets the worktree.

## Interaction

An open/current local PR starts in detail. The default branch, detached HEAD, or a branch without local PR context starts in the cached PR list. Returning from local detail adds a `Local PR` entry even before publication.

- list views: `[`/`]` switches Assigned (default), Review requested, All, Authored, Needs me, and Closed; Closed is loaded lazily once and then retained alongside Open, so moving between cached views does not refetch or clear their counts; `/` live-filters with `is:open`, `is:closed`, `author:`, `assignee:`, `review-requested:`, `label:`, `draft:`, `ci:`, and `merge:` terms;
- stacks: exact `child.baseRefName == parent.headRefName` relationships are grouped without title heuristics; `Space` collapses/expands the selected stack;
- list: `j`/`k` selects and updates the right metadata/Conversation preview; `gg`/`G` move the PR selection to top/bottom, while `Ctrl+U`/`Ctrl+D` scroll the preview; `Enter` opens without checkout; `c` checks out, `x` closes, and `m` merge-commits through a centered confirmation popup; `y` confirms and `n`/`Esc` cancels; loading work shows an animated indicator; `r` refreshes and `q` exits;
- preview: bordered opening-description/top-comment cards, ownership/labels, CI, merge/conflict/review state, comments, files/additions/deletions, and commit count;
- detail: reserved `b` returns to the list from either pane; `j`/`k` selects Conversation items, `Ctrl+U`/`Ctrl+D` scrolls the focused Conversation, and `m` requests a merge when the current PR is open and has a verified head commit;
- `c` while the left pane is focused replaces Conversation with the local commit picker; in static diff modes it toggles the selected file's check; in CodeReview, `c` toggles the selected file's reviewed check; CodeReview marks persist in Neovim user state and automatically clear when that file's `base...head` diff changes;
- static `git diff`/`display` modes keep Conversation in the left pane and show `Explorer │ Diff` in the right pane; focus Explorer with `l`; Diff is always visible and Explorer `l` is a no-op; `Ctrl+U/D` scrolls the Diff, then `j`/`k` changes the selected file and updates the diff immediately, while `c` toggles its checked mark;
- `j`/`k` selects a commit;
- `Enter` restarts the right pane with `commit_command` and `LIVE_PR_SHA`, then focuses it;
- `l` focuses the right review pane; `q` returns to the left from review, and exits live-pr when already on the left;
- `Shift+Tab` remains the shared focus toggle;
- `Esc` from the commit picker restores Conversation and branch-wide `command` review;
- clicking either pane also changes focus;
- while CodeReview has focus, keys other than reserved `q` and `Shift+Tab` are sent directly to Neovim;
- the active CodeReview border uses the accent color.

The PTY is resized with the right pane. live-pr runtime state is kept in the XDG state directory rather than `.live-pr/` in the repository; old repository-local state is migrated on first access.

Each branch/commit switch closes and reaps the previous process, and generation-specific IDs discard its late messages.

## Fallback display

When the command for the current scope is disabled, unsupported, exits, or fails, live-pr keeps a matching built-in view:

```text
branch scope → git diff base...head revision
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

- `internal/git`: explicit local/remote ranges and namespaced pull-ref fetches;
- `internal/github`: repository PR-list/snapshot cache separate from branch publish state;
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
