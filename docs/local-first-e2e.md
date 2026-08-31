# Local-first end-to-end regression checklist

Use this checklist before a release that changes local review, Git polling, or
GitHub synchronization. Run it in a disposable repository or with
`live-pr demo`; several steps create branches and worktrees.

## Automated baseline

```sh
go test ./...
go test -race ./internal/github ./internal/tui \
  -run 'CacheClone|AsyncCacheSave|CachedDetail|CIPollBackoff|LocalStatePoll'
go test ./internal/git -run \
  'TestLinkedWorktree|TestDetached|TestShallow|TestCurrentLocalSnapshot'
go test ./internal/tui -run TestLocalReviewStateLifecycle
```

The normal CI matrix must pass on Linux, macOS, and Windows. The edge-state
Git tests use native Go paths and Git subprocesses, including spaces and
Unicode; they must not be skipped on Windows.

## Core state lifecycle

The default demo contains published, local-only, remote-only, and untracked
content without touching GitHub:

```sh
live-pr demo
```

1. Confirm the status line says `LOCAL`, includes ahead/behind counts, and says `diverged`.
2. Press `c`; confirm `Published on PR`, `Local only`, `Remote only`, and `Working tree` are present.
3. Select one row in each commit section and press `Enter`; confirm the selected commit diff opens.
4. Select `Working tree`; confirm the untracked file appears.
5. Press `b`, open the other open PR, and confirm the mode becomes `REMOTE`.
6. Return to the checked-out PR and confirm the prior tab, cursor, focus, and viewport are retained.

For a real PR, additionally verify this sequence:

| Action | Expected state |
| --- | --- |
| Clean checkout equals published head | `LIVE` |
| Edit, stage, or add an untracked file | `LOCAL · dirty` |
| Commit without pushing | `LOCAL · N ahead` |
| Push and press `r` | `LIVE` |
| Update the PR from another clone | `LOCAL · N behind · remote update` |
| Force-push a different history, then press `r` | `LOCAL · N ahead · M behind · diverged` |

Remote updates must not replace the active diff until `r` is pressed.

## Offline and concurrent refresh

1. Open a `LIVE` PR, then disconnect the network or block access to GitHub.
2. Confirm local commits and diffs remain usable and cached conversation remains visible.
3. Confirm retry status advances through 30 seconds, one minute, then two minutes.
4. Restore connectivity and press `r`; confirm the retry state clears immediately.
5. While a refresh is running, edit a file and switch branches in another terminal.
6. Confirm the new branch wins, stale responses do not restore the previous PR, and the spinner stops.
7. Press `r` repeatedly during a local reload; confirm only one full refresh is active and no selection is lost.

## Repository edge states

### Linked worktree

```sh
git worktree add ../live-pr-e2e-worktree -b live-pr-e2e
cd ../live-pr-e2e-worktree
live-pr
```

Confirm local changes are detected in the linked checkout and review state is
stored under the same repository identity as the main checkout. Remove the
disposable worktree afterward from the main checkout.

### Detached HEAD

```sh
git switch --detach HEAD
live-pr
```

Confirm a non-default revision opens as `LOCAL`, GitHub branch lookup and
publish are disabled, and local edits still refresh automatically.

### Shallow clone

```sh
git clone --depth=1 <repository-url> live-pr-e2e-shallow
cd live-pr-e2e-shallow
live-pr
```

Open or check out a PR. Confirm its namespaced pull ref is fetched without
changing the checkout and local files remain reviewable even when older merge
history is unavailable.

### Fork PR

Open a fork PR whose head branch has the same name as a local branch. Confirm
it stays `REMOTE` until explicitly checked out. Checkout must use the
GitHub-aware path, not assume the head exists on `origin`.

## Terminal and platform checks

Repeat the core demo in a terminal approximately 60 columns by 20 rows:

- the mode remains visible;
- retry/error/remote-update text takes priority over PR metadata;
- the footer never exceeds the terminal width;
- commit headings and selectable rows remain aligned;
- mouse selection matches keyboard selection.

On Windows, run once from PowerShell and once from Git Bash when available.
Confirm drive-letter paths, path separators, spaces, Unicode filenames, and
process cancellation behave the same as on Linux/macOS. On all platforms,
quit during an in-flight refresh and confirm no Git or `gh` child process is
left running.

## Pass criteria

A release passes when the automated baseline and CI matrix are green, every
manual item above matches its expected state, no active review selection is
lost during refresh, and no test repository or worktree is modified outside
the explicitly requested operation.
