# Local review modes

live-pr shows the source of the review in the left side of the status line.
The pane titles already identify the current UI view; the mode identifies which
repository state the review represents.

## Modes

| Mode | Meaning | Diff source |
| --- | --- | --- |
| `LOCAL` | The checked-out branch has no PR, has commits not on the PR, or has index/worktree changes. | Local `HEAD`, index, worktree, and untracked files |
| `LIVE` | The checked-out branch has a PR, local `HEAD` equals the published PR head, and the worktree is clean. | Local Git, with GitHub metadata layered on top |
| `REMOTE` | A PR that is not the checked-out branch is open from the PR list. | The fetched `refs/live-pr/pulls/<number>/head` ref |

A PR can move from `LIVE` to `LOCAL` without closing the detail screen. A local
commit or file edit makes the checkout local. A changed remote head also leaves
`LIVE` and asks for an explicit refresh rather than replacing a review while it
is being read.

## Local-first data ownership

For the checked-out branch, local Git is authoritative for:

- commits, subjects, and commit dates;
- changed paths, renames, and per-file diffs;
- additions, deletions, and file counts;
- merge base, ahead/behind information, and conflict simulation;
- staged, unstaged, and untracked content.

GitHub remains authoritative for:

- PR title, body, state, and draft state;
- comments, reviews, inline review comments, labels, and assignees;
- linked issues and CI/check results.

The local detail request omits GitHub diff statistics and remote commit text.
It retains commit OIDs so CI rollups can be matched to local commits.

## Automatic refresh

While a checked-out branch detail is open, live-pr checks a lightweight Git
fingerprint every two seconds. The fingerprint includes `HEAD`, branch, index,
worktree, and untracked state. A full local scan runs only when that fingerprint
changes. Same-branch reloads retain the active tab, cursor, focus, and viewport.
An external branch checkout rebuilds the model for the new branch.

When the mode is `LIVE`, live-pr also polls lightweight GitHub head, PR state,
draft state, and check metadata every 15 seconds. Failures use exponential
backoff. `LOCAL` and `REMOTE` details do not run this remote poll.

Press `r` for an explicit GitHub refresh. Remote head changes are never applied
to the active local diff implicitly.

## Commit and file views

The commit picker separates commits already published on the PR from commits
that exist only in the checkout. Local-only commits remain reviewable with
`git show`. File review always includes the latest staged, unstaged, and
untracked content for `LOCAL` and `LIVE` modes.

## Cache and offline behavior

GitHub metadata is cache-first. Cached conversation and PR metadata render
immediately, then refresh in the background at startup or when explicitly
requested. If GitHub is unavailable, local commits and diffs remain usable and
the status line reports that cached GitHub data is being shown. Authentication
and setup errors are reported separately from network failures.
