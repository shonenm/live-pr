# Development

## Building and checks

```sh
GOTOOLCHAIN=go1.26.10 just check  # tests, race, lint, vulnerability scan, module and diff checks
go build -o live-pr .
```

Use the Go patch version in `go.mod` for checks and release-size comparisons.
Go 1.26.10 fixes GO-2026-6809; the pinned linter cannot read Go 1.27 export data.
The vulnerability gate checks reachable symbols. Non-reachable module findings
are reported separately and do not mean the executable is affected.

Set `LIVE_PR_DEBUG_TIMING=1` to print opt-in startup, Git, GitHub,
cache-save, and TUI synchronization timings to stderr.

## Rendering budgets and measurements

Markdown output caching retains at most 512 entries and 8 MiB of input/output
payloads. Entries larger than 256 KiB are rendered but not cached. FIFO eviction
releases both input and output; the budget is not a limit on transient renderer
allocations or the entire process heap. Concurrent duplicate inserts count once.

On Apple M2 with Go 1.26.10, `BenchmarkRender/cache-hit` changed from about
731 ns / 256 B to 351 ns / 128 B after removing redundant input sanitization.
A 1 MiB `BenchmarkRichContentKey` changed from about 691 µs / 3.17 MB to
430 µs / 0 B using a bounded streaming hash buffer. Hashing still scans all bytes;
no additional invalidation cache was introduced.

`BenchmarkApplyPRPage` remained about 0.22 ms for 25 rows and 19.5 ms for 2,500
rows. The latter is beyond the normal page size. No extra PR-list caching was
added without a measured normal-page bottleneck. Reproduce these measurements
with the existing benchmarks, keeping Go version, fixture and hardware fixed:

```sh
GOTOOLCHAIN=go1.26.10 go test ./internal/markdown -run '^$' -bench BenchmarkRender -benchmem
GOTOOLCHAIN=go1.26.10 go test ./internal/tui -run '^$' -bench 'BenchmarkRichContentKey|BenchmarkApplyPRPage' -benchmem
```

## Local state and errors

Timeline commit synchronization locks its load/check/append transaction across
processes, just like outbox updates. Sidecar `.lock` files are intentionally
retained; deleting an active lock file would break mutual exclusion. Ordinary
notes remain append-only and are not replaced by commit synchronization.

TUI branch and navigator cache saves use dispatch-ordered snapshots. A delayed
older command cannot replace a newer successfully saved snapshot. This ordering
is shared across checkout reloads within one TUI; it is not a cross-process
merge policy. Files still use temporary-file replacement to avoid partial JSON.

Local startup and refresh share the same data loader. Failed auxiliary reads
are reported as incomplete data, with unavailable diff statistics and revision
distance distinguished from zero. A same-target reload keeps valid previous
fields when their replacement could not be read; switching targets never
carries those fields across. Missing conclusion files are normal, unlike I/O
errors. Automatic PR resolution reports cache read failures and retains only
matching in-memory PR data.

TUI commands share a lifetime context, including Git/GitHub, local hydration,
checkout rebuilds, static diff filters, configured CI, avatar fetching, and
Mermaid rendering. Closing the application cancels this context and suppresses
late UI updates. Replacing the Model closes only its view resources and retains
the lifetime. Unix cancellation kills the process group; Windows uses native
`taskkill /T` with direct-process termination if tree cleanup fails. Output-pipe
waits are bounded. Browser applications opened explicitly remain independent.

Cancellation is not a rollback of a push, comment, review, or publish that may
already have reached the server. The application does not automatically repeat
such operations on cancellation; inspect remote state before retrying.

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
  once a view is fetched (`?` means unvisited). GitHub advanced search handles
  OR groups and qualifiers such as `reviewed-by:@me` server-side. `ci:`,
  `merge:`, and OR groups containing them are local post-filters. The `/`
  filter is ANDed with the active view's query; use All to search without
  a view restriction. GitHub Search caps any query at its first 1,000 results.
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
