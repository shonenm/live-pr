# Performance audit

## Scope and method

This report audits the full `live-pr` repository at commit `2336bf2`.

- A `codebase-audit` workflow ran 10 agents across GitHub access, Git/subprocess work, TUI rendering, caching, memory bounds, dependencies, and measurement gaps.
- A cross-validation pass rejected or weakened claims that were only speculative.
- The production paths were then checked directly in the repository.
- `go test ./...`, `go vet ./...`, and `git diff --check` passed before this report was written.

No benchmark, pprof profile, runtime trace, or end-to-end latency capture exists yet. Therefore this report distinguishes **confirmed repeated work** from **bottleneck hypotheses**. Code structure can prove that work occurs; it cannot prove how much wall-clock time that work consumes on representative repositories.

## Executive summary

`live-pr` already has good performance fundamentals: cache-first startup, lightweight PR-list queries, lazy previews, stale-result rejection, bounded diff output, Markdown caching, PTY burst draining, and stateful Open/Closed list caching.

The highest-value remaining opportunities are:

1. avoid repeated raw `git diff` / `git show` subprocesses during detail synchronization;
2. reduce synchronous Git work before the first useful frame;
3. parallelize independent GitHub detail requests;
4. reduce large-repository GraphQL work, especially unnecessary review-request search for Closed;
5. measure and then reduce repeated O(n) Conversation and PR-list derivation;
6. bound persistent/session caches and avoid rewriting a growing navigator cache on every preview.

The spinner, key dispatch, mouse translation, atomic cache writes, and PTY output coalescing are not priority optimization targets.

## Existing optimizations to preserve

| Area | Existing behavior | References |
|---|---|---|
| PR startup | Cached PR metadata is rendered before background refresh | `internal/tui.New`, `Model.Init` |
| PR list | Body, comments, commits, and detailed checks are excluded from list rows | `internal/github.Client.ListState` |
| PR preview | Heavy metadata is loaded only for the selected PR and stale generations are rejected | `ensureSelectedPRPreview`, `prPreviewLoaded` |
| View state | Open and Closed results are retained and reused after first fetch | `applyPRViewState`, `NavigatorCache.FetchedStates` |
| Static formatter | Raw diff appears first; formatted output is cached and stale output is discarded | `syncDetail`, `internal/diffview` |
| Formatter safety | Timeout, bounded stdout/stderr, process-tree cancellation, and output truncation | `internal/diffview/diffview.go` |
| Markdown | Rendered output is cached by width and text | `internal/markdown/render.go` |
| PTY | Scrollback is bounded and buffered output is drained in small bursts | `internal/embeddedterm/terminal.go` |
| Persistence | JSON replacement is atomic through temp-file + rename | `internal/github/cache.go` |

## Prioritized findings

### P0 — Measure before broad optimization

#### 1. There is no reproducible performance baseline

**Evidence:** no `Benchmark*`, pprof, or runtime trace code exists in the repository.

Without a baseline, micro-optimizations could increase complexity without improving user-visible latency. Add focused measurements before changing data structures broadly.

Recommended baseline scenarios:

- startup on a branch with 10, 100, and 1,000 changed files;
- PR list with 25, 250, and 2,500 cached rows;
- Conversation with 10, 100, and 1,000 combined events/comments/activities;
- static diff navigation across small and large files;
- GitHub detail refresh with injected 100–500 ms request latency;
- navigator cache at 100 KiB, 1 MiB, and 10 MiB.

Minimum measurements:

- time to first frame;
- time to interactive detail/list;
- key-to-frame latency for `j`/`k`;
- subprocess count per interaction;
- allocations for list/Conversation derivation;
- navigator cache serialization time and size.

Do not add a permanent metrics subsystem yet. Focused Go benchmarks plus an opt-in timing/debug mode are sufficient.

### P1 — High-confidence, high-impact candidates

#### 2. Detail synchronization repeats raw Git subprocesses

**Evidence:** `Model.sync()` calls `syncDetail(m.buildDetail())`. `buildDetail()` calls `git.FileDiffRange`, and commit detail calls `git.Show`. The existing `diffCache` caches formatter output, not the raw Git result.

**Why it matters:** a synchronization caused by selection, resize, focus, or another state update can repeat `git diff`/`git show` even when revision and selected file have not changed. Process startup and diff generation dominate string-level rendering work for large diffs.

**Recommendation:** cache raw detail by the existing identity:

- branch range: base + head revision;
- file range: base + head revision + status + old/new paths;
- commit: SHA.

Invalidate on target generation/range change. Keep the formatter cache separate because it also depends on command and width.

**Validate with:** subprocess-count tests and key-to-frame latency before/after.

#### 3. Startup performs synchronous Git work before the first useful frame

**Evidence:** `tui.New()` synchronously resolves repository/branch/base/cache state and calls `git.ChangedFiles(base)` for local-detail routing. `loadLocal()` then performs timeline sync, event load, commit enumeration, changed-file enumeration, and diff statistics.

**Why it matters:** startup cost grows with repository and branch-diff size. Some work is duplicated between routing and local-detail initialization.

**Recommendation:** split startup into:

1. minimal repository/cache model sufficient to render;
2. cheap “has changes” check for routing;
3. asynchronous commit/file/stat hydration only when local detail is selected.

A native `git diff --quiet base...HEAD`-style existence check is preferable to enumerating every changed path solely to decide the initial screen.

**Validate with:** time-to-first-frame and Git subprocess count on large synthetic branches.

#### 4. GitHub detail requests are independent but sequential

**Evidence:** `fetchGitHub` and `fetchRemotePR` obtain issue comments and activities one after the other. Remote loading also fetches refs before returning the combined result.

**Why it matters:** independent network waits add rather than overlap.

**Recommendation:** run comments and activities concurrently inside the existing Bubble Tea command, retaining their separate errors and partial-success behavior. Decide separately whether pull-ref fetch should overlap; correctness checks around the advertised OID must remain.

**Validate with:** the stateful demo mock using fixed latency per endpoint. Expected detail latency should approach the slowest independent request rather than their sum.

#### 5. Large-repository PR listing performs avoidable work

**Evidence:** `Client.ListState` first calls `gh repo view`, then paginates PR rows in groups of 25 while also paginating a `review-requested:@me` search. The same open-only review query is included when listing Closed.

**Why it matters:** list latency grows linearly with page count, and Closed pays for data that cannot contribute meaningfully to active review-request views.

**Recommendations, in order:**

1. omit review-request search for Closed;
2. cache repository owner/name for the client/session instead of calling `gh repo view` on every refresh;
3. measure GraphQL resolver time before changing the 25-row page size;
4. investigate bounded/incremental retrieval only if full pagination remains a measured problem.

Do not put heavy preview fields back into the list query.

#### 6. Selected preview fetch retrieves complete collections

**Evidence:** `FindPreview` asks `gh pr view` for full comments, commits, and check rollup arrays, then uses their lengths and compact preview data. This is lazy, so it does not affect initial list loading, but it affects cursor-to-preview latency for unusually large PRs.

**Recommendation:** if measurement confirms large-preview latency, replace this with a bounded GraphQL preview that requests totals plus only the comments/check details actually rendered. Preserve a separate full detail path where required.

**Validate with:** PR fixtures containing hundreds/thousands of comments, commits, and checks.

### P2 — Scale-sensitive CPU and persistence work

#### 7. Conversation derivation is repeatedly rebuilt and sorted

**Evidence:** `conversationItems()` allocates and combines description, local events, comments, and activities, deduplicates entries, and sorts them. Selection/key restoration and rendering call this derivation from multiple paths.

**Why it matters:** update cost becomes O(n log n) for long-lived Conversations.

**Recommendation:** derive Conversation items once when source data changes, store them on the model, and reuse them for selection, length, and rendering. Do this only after a benchmark demonstrates meaningful cost; cache invalidation complexity is otherwise not justified.

#### 8. PR view counts and rows rescan the full list on synchronization

**Evidence:** the header calls `viewCount` for every view, and list row/stack rendering traverses the selected set again. This occurs on `sync()` updates, not on every terminal frame.

**Why it matters:** large cached PR sets can make navigation updates allocation-heavy.

**Recommendation:** compute per-view counts while applying filters/state updates and reuse them until PR metadata/viewer identity changes. Incremental row rendering is probably unnecessary unless benchmarks still show latency after count caching.

#### 9. Navigator cache grows without a retention policy and is rewritten synchronously

**Evidence:** `NavigatorCache` retains PR rows and an unbounded snapshot map. List refresh, preview load, and remote detail load call `SaveNavigatorCache`; `saveJSON` marshals and rewrites the whole value atomically.

**Why it matters:** browsing more PRs increases startup decode time, memory, and synchronous serialization/write cost.

**Recommendation:** first measure real cache sizes. If growth is material:

- retain only the newest/recently viewed snapshots;
- avoid duplicating large preview fields between row and snapshot data;
- save only when content changed;
- consider debouncing non-critical preview saves.

Do not remove atomic rename. It protects against cache corruption.

#### 10. Timeline synchronization rereads append-only history

**Evidence:** `timeline.SyncCommits` loads the complete event file to build a SHA set and then queries all base..HEAD commits. Local loading subsequently reads event data for display.

**Why it matters:** cost grows with long agent sessions and branch history.

**Recommendation:** measure representative timeline sizes. If material, pass already-loaded events into synchronization or maintain a compact commit index. Avoid adding a second database/index before file size proves this necessary.

#### 11. Transcript tail bounding happens after full accumulation

**Evidence:** `transcript.Text` scans and extracts the entire JSONL transcript into a `strings.Builder`, then retains only the newest `maxBytes` (40 KiB for the hook).

**Why it matters:** output is bounded, but peak memory and parsing time are not.

**Recommendation:** use a bounded tail/ring of extracted blocks if production transcripts are large enough to affect hook latency. Preserve complete JSONL line validation and the scanner’s input limit.

#### 12. Session maps have no explicit limits

**Evidence:** formatter results in `diffCache` vary by command, width, and detail identity; checked-file state also remains for the session.

**Why it matters:** repeated resizing and browsing many files/commits can retain multiple large formatted diffs.

**Recommendation:** measure peak session memory. If needed, clear caches on target generation change and cap formatter entries by count/bytes. Checked-file state is comparatively small and should not receive an elaborate eviction policy.

### P3 — Responsiveness and secondary opportunities

#### 13. Git subprocesses have no timeout or cancellation

**Evidence:** `internal/git/git.go` uses `exec.Command(...).Output()`/`CombinedOutput()` for log, diff, show, and fetch. GitHub commands have a 30-second timeout, and static formatter processes have timeout/tree cancellation.

**Why it matters:** this is primarily a responsiveness and failure-containment issue rather than a throughput optimization. A blocked Git process can freeze synchronous startup/detail work.

**Recommendation:** introduce context-aware Git execution where calls can be asynchronous/cancelled. Use operation-specific timeouts; do not apply an aggressive universal timeout to legitimate large fetches.

#### 14. Markdown renderer creation occurs on cache misses

**Evidence:** `markdown.Render` creates a new Glamour renderer for each width+text cache miss. The output cache is bounded at 512 entries and clears as a whole.

**Recommendation:** leave this alone until cache-miss profiles show a problem. Renderer reuse by width may help, but renderer/thread-safety and style mutability must be verified first.

## Do not optimize now

- **Spinner ticks and key/mouse dispatch:** small relative to Git/network/render work.
- **Atomic temp-file + rename:** correctness benefit outweighs speculative I/O savings.
- **Diff output bounds/timeouts:** these prevent runaway memory/processes.
- **PTY scrollback and burst draining:** already bounded and intentionally reduce redraws.
- **Lazy preview architecture:** keep it; optimize preview payload only if measured.
- **Markdown dependency replacement:** a product/quality tradeoff, not justified by current evidence.
- **Complex incremental TUI rendering:** cache obvious derived values first.

## Recommended execution plan

### Now: establish evidence

1. Add focused benchmarks for Conversation derivation, view counts/list rows, Markdown hit/miss, and navigator serialization.
2. Add an opt-in debug timing mode for startup stages, Git commands, GitHub calls, and key-to-sync latency.
3. Capture one synthetic large-repository profile and one real-repository profile.

### Next: low-risk structural wins

1. cache raw Git detail by immutable range/file/commit identity;
2. parallelize comments and activities fetches;
3. remove review-request search from Closed and cache repository identity;
4. replace startup changed-file enumeration with a cheap change-existence check.

### Then: only if measurements justify it

1. bound/reshape preview GraphQL payloads;
2. cache Conversation derivation and view counts;
3. add navigator snapshot retention/debounced persistence;
4. stream-bound transcript extraction;
5. cap session diff caches.

## Acceptance criteria for future optimization PRs

Every optimization should include:

- a reproducible before/after benchmark or timing capture;
- unchanged cache invalidation and stale-generation semantics;
- focused tests for correctness and cancellation;
- memory/allocation numbers where caching is added;
- a stated tradeoff and rollback condition.

A change should not merge solely because it removes allocations or subprocesses in theory; it should improve one of the user-visible scenarios listed in the baseline section.
