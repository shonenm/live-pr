# Development

## Building and checks

```sh
just check          # tests, targeted race checks, vet, module verification, working-tree diff check
go build -o live-pr .
```

Set `LIVE_PR_DEBUG_TIMING=1` to print opt-in startup, Git, GitHub,
cache-save, and TUI synchronization timings to stderr.

## CLI reference

```sh
live-pr                   # open the TUI for the current repository and branch
live-pr -C ../repo        # run any command against another working directory
live-pr --diff=delta      # override the review pane: git, delta, codediff, codereview, or a command

live-pr status            # cache-first local/GitHub PR status
live-pr status --json     # machine-readable; add --refresh to query GitHub

live-pr init              # seed the final summary from the repo PR template
live-pr init --hooks      # print the optional Claude Code Stop hook
live-pr sync              # import base..HEAD commits into the timeline

live-pr summary edit      # edit the final result in $VISUAL/$EDITOR
live-pr summary set --file summary.md

live-pr comment add "Use GraphQL" --kind decision --body "Avoids repeated requests"
live-pr comment list --json
live-pr comment edit <id> "Use batched GraphQL" --body "One request"
live-pr comment delete <id>

live-pr review body --body "General review feedback"
live-pr review add internal/app.go --line 42 --side RIGHT --body "Handle this error."
live-pr review show --json
live-pr review submit --event REQUEST_CHANGES   # or COMMENT / APPROVE

live-pr pr preview        # preview the generated managed PR body
live-pr pr publish        # push and create or update the GitHub PR
```

## Demos

```sh
live-pr demo              # built-in git diff
live-pr demo delta        # delta (unified)
live-pr demo delta-side   # delta (side-by-side)
live-pr demo codereview   # embedded Neovim CodeDiff
```

Demos build a disposable repository with stateful mock PRs; no GitHub
resources are created. The current PR conversation includes mocked CI
activity (one red, one green commit); `b` opens the mocked PR list where
`m` / `c` / `x` exercise merge, checkout, and close. Avatar colors and
Mermaid rendering (with `termaid` installed) are included.

## Behavior notes

- The PR navigator fetches only the active view's first 25 rows; reaching
  the final loaded row requests the next page. View counts become exact
  once a view is fetched (`?` means unvisited). Search runs server-side;
  `ci:` and `merge:` are local post-filters. GitHub Search caps any query
  at its first 1,000 results.
- Review drafts live under repository XDG state, isolated by PR number and
  head commit. Submission re-checks the GitHub head SHA and refuses stale
  line comments after a push.
- Pending CI refreshes every 15 seconds while the PR detail stays open,
  then stops on completion.
- Runtime state lives under the XDG state directory
  (`~/.local/state/live-pr` on Linux). Repo-specific configuration uses
  `.live-pr.toml`.
- `LIVE_PR_RANGE` is a merge-base-to-working-tree comparison for
  checked-out local PRs, or the historical base-to-fetched-head three-dot
  range for remote PRs. Selected commits use the commit's parent-to-commit
  range. See [diff-tool-integration.md](diff-tool-integration.md).

## Releasing

See [releasing.md](releasing.md).
