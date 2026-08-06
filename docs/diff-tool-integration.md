# Diff tool integration design

## Goal

Keep the TUI instant and useful without external tools, while allowing users to:

1. transform the embedded diff with a non-interactive renderer such as `delta`;
2. open the selected commit, file, or full range in an interactive reviewer such as Neovim or a browser-based diff tool.

The built-in `git diff` / `git show` output remains the fallback and the default.

## Two integration classes

### Embedded renderer

A renderer is a non-interactive filter. live-pr sends the already-bounded ANSI diff to stdin and replaces the detail pane when the renderer finishes.

```toml
[diff]
renderer = "delta --color-only"
```

Behavior:

- show the raw local Git diff immediately;
- start the renderer only for the selected item;
- replace the pane only if the selection is still current;
- keep a small in-memory cache keyed by tool command and diff identity;
- enforce output and execution limits;
- retain the raw diff and show a footer warning when the renderer fails.

No renderer runs during startup for unselected files or commits.

### Interactive reviewer

A reviewer owns the terminal temporarily through `tea.ExecProcess` and returns to live-pr when it exits. Commands are scoped because commit, file, and range review need different arguments.

```toml
[diff]
review_commit = 'nvim -c "CodeDiff $LIVE_PR_SHA~1 $LIVE_PR_SHA"'
review_file = 'nvim -c "CodeDiff $LIVE_PR_BASE $LIVE_PR_HEAD" -- "$LIVE_PR_FILE"'
review_range = 'nvim -c "CodeDiff $LIVE_PR_BASE $LIVE_PR_HEAD"'
```

Environment variables:

| Variable | Meaning |
| --- | --- |
| `LIVE_PR_SHA` | selected commit SHA |
| `LIVE_PR_BASE` | comparison base |
| `LIVE_PR_HEAD` | current head branch |
| `LIVE_PR_FILE` | selected new/current path |
| `LIVE_PR_OLD_FILE` | old path for rename/copy |
| `LIVE_PR_PR_URL` | linked PR URL, when available |

Values are passed as environment variables rather than interpolated into shell text. Commands keep the existing `sh -c` contract so quotes and pipes remain supported; every variable used as an argument must be double-quoted. This avoids treating branch names and file paths as shell syntax.

## Backward compatibility

The existing flat setting remains valid:

```toml
reviewer = 'nvim -c "CodeDiff {sha}~1 {sha}"'
```

Resolution order:

1. use the matching `[diff].review_*` command;
2. fall back to the legacy `reviewer` for commit scope;
3. leave Enter disabled when no command exists for the active scope.

Legacy `{sha}`, `{base}`, and `{head}` substitution remains supported for backward compatibility. New configuration should use environment variables.

## TUI behavior

| Context | Embedded detail | Enter |
| --- | --- | --- |
| Conversation commit | commit patch | `review_commit` |
| Files changed | selected file patch | `review_file` |
| Commits | commit patch | `review_commit` |
| Files changed tab with no selection | placeholder | disabled |
| Full range action | unchanged current pane | `review_range` |

Suggested keys:

- `Enter`: reviewer for the current selection;
- `D`: reviewer for the complete base-to-head range;
- raw/renderer switching is unnecessary initially; renderer failure already falls back to raw output.

## Execution and caching

- Embedded renderer: background `tea.Cmd`, non-interactive stdin/stdout, two-second default timeout, existing 800-line output ceiling.
- Interactive reviewer: foreground `tea.ExecProcess`, no arbitrary timeout.
- Renderer cache: memory-only; key by command + commit SHA, or command + base/head/file paths.
- Selection changes never wait for a previous renderer. Late results whose key no longer matches are discarded.
- No rendered diff is persisted in `github.json`; it is derived local state.

## Error UX

- Missing renderer: raw diff remains visible; footer reports the command error once.
- Missing scoped reviewer: Enter is disabled and omitted from help.
- Reviewer non-zero exit: return to the same tab, cursor, and scroll position with a footer error.
- Binary or empty diff: show the existing placeholder and do not invoke the renderer.

## Implementation seams

1. Extend `internal/config.Config` with a nested `Diff` struct while retaining `Reviewer`.
2. Replace direct placeholder expansion in `internal/review` with a scope-aware command builder and environment variables.
3. Add renderer execution and result messages to `internal/tui`; keep raw `internal/git` output unchanged.
4. Enable reviewer bindings per active tab and available scoped command.
5. Add tests for config fallback, environment propagation, file rename paths, late renderer results, timeout/failure fallback, and tab state restoration.

## Non-goals

- No Go plugin API.
- No tool-specific dependency or bundled diff executable.
- No daemon or persistent rendered-diff cache.
- No parsing of third-party renderer output back into structured hunks.
- No inline GitHub review-comment creation in this integration layer.
