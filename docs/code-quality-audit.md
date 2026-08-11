# Code quality audit

## Scope and method

This report audits the full `live-pr` repository at commit `2336bf2` for:

- readability and naming;
- function and file decomposition;
- directory/package structure;
- separation of state, rendering, persistence, and external I/O;
- idiomatic Go and API contracts;
- test maintainability;
- duplication, dead code, and unnecessary abstraction;
- documentation and operational maintainability.

A `codebase-audit` workflow ran 12 agents over production code, tests, commands, scripts, workflows, and docs. A separate cross-validation pass checked material findings and rejected findings that were only naming taste. The strongest findings were then verified directly in source.

`go test ./...` and `go vet ./...` passed during the workflow audit. This is a maintainability audit, not a request to rewrite working code.

## Executive summary

The repository is **structurally healthy**. Most packages are cohesive, dependency direction is simple, CLI commands are thin, important state is persisted atomically, and tests cover many failure/state-transition paths. There is little speculative abstraction or dependency-driven complexity.

The main maintainability debt is concentrated in one place:

- `internal/tui/tui.go` is 3,011 lines;
- `Model.Update` spans roughly 700 lines;
- model state, message routing, key handling, Git/GitHub orchestration, persistence, derived data, and rendering share one file;
- several names that imply pure view construction (`buildDetail`, `BuildPreview`) perform I/O or mutate persistent state.

This concentration has not made the code broadly poor. It has made future TUI changes harder to reason about locally. The right response is incremental extraction along existing seams, not a new framework, controller hierarchy, or package rewrite.

There are also a few objective, focused issues worth fixing independently:

1. malformed config and some read errors are silently ignored;
2. publish preview mutates the timeline and discards synchronization/read errors;
3. transcript truncation can split UTF-8;
4. Git and summarizer errors often lose stderr/context;
5. small dead/duplicated code and incorrect exported comments remain.

## Quality map

| Area | Current assessment | Recommendation |
|---|---|---|
| `cmd` | Thin and direct | Keep |
| `internal/event` | Small append/load responsibility | Keep |
| `internal/store` | Clear XDG/branch-state ownership | Keep |
| `internal/config` | Small API, but silent error policy is unclear | Fix error contract |
| `internal/git` | Simple native adapter, limited diagnostics/cancellation | Improve runner when touched |
| `internal/github` | Coherent adapter/cache area, with one dense list method | Extract locally when list logic changes |
| `internal/timeline` | Small and understandable | Keep; revisit persistent SHA format only with migration work |
| `internal/prbody` | Strong ownership/conflict boundary | Keep |
| `internal/publish` | Mostly cohesive, preview has hidden mutation | Separate sync from assembly |
| `internal/diffview` | Clear safety boundary | Keep |
| `internal/embeddedterm` | Clear PTY lifecycle boundary | Keep |
| `internal/markdown` | Focused adapter with bounded cache | Keep |
| `internal/transcript` | Focused, one truncation defect | Fix locally |
| `internal/summarize` | Small, but process errors and parsing need focused tests | Improve locally |
| `internal/tui` | Functional and tested, but overloaded | Refactor incrementally |
| demo code | Correctly isolated from production, script is dense | Leave until changed |
| tests | Broad behavioral coverage, TUI tests are white-box-heavy | Add focused boundary tests; avoid rewrite |

## Strengths to preserve

### 1. Package boundaries mostly follow real responsibilities

The repository avoids broad “service”, “manager”, or “utils” packages. Names such as `event`, `timeline`, `prbody`, `publish`, `diffview`, and `embeddedterm` identify concrete domain or platform responsibilities.

Import direction is straightforward:

- `cmd` delegates to `internal` packages;
- small domain packages do not depend on the TUI;
- `tui` acts as the application composition layer;
- no import cycles or artificial interfaces were found.

Do not reorganize directories merely to make the tree more layered. The current structure is easier to navigate than an `application/domain/infrastructure` rewrite would be for this project size.

### 2. External integrations use native, explicit boundaries

- Git is wrapped in `internal/git` instead of introducing a large Git library.
- GitHub access is centralized in `internal/github` and already has command timeout behavior.
- diff formatting is isolated in `internal/diffview` with bounded output and process cancellation.
- CodeReview PTY lifecycle is isolated in `internal/embeddedterm`.

These boundaries are practical and testable. Keep them.

### 3. State ownership is intentionally separated

The distinction between:

- branch-local publish/conflict cache; and
- repository-wide navigator/list/snapshot cache

is meaningful, not duplication. It protects publish conflict semantics while allowing cache-first navigation. Do not merge these cache models merely to reduce type count.

### 4. Safety-critical code is explicit

Good examples include:

- managed PR body ownership and conflict hashing in `internal/prbody`;
- atomic JSON replacement in `internal/github/cache.go`;
- stale async generation checks in `internal/tui`;
- bounded formatter output and timeout behavior in `internal/diffview`;
- explicit PTY close/reap ownership in `internal/embeddedterm`.

These areas favor boring, visible control flow over clever abstraction. That is appropriate.

### 5. Tests capture important behavior

The suite covers PR list/detail transitions, stale results, checkout/merge/close flows, cache migration, publish conflict behavior, diff fallback, PTY lifecycle, and cross-platform boundaries. This gives the codebase a strong base for incremental cleanup.

## Fix now — objective, focused issues

### 1. Config parsing and read errors are silently discarded

**Location:** `internal/config/config.go`, `Load`

`Load` returns only `Config`. For each config path it applies data only when `os.ReadFile` succeeds and ignores every other read error. It also discards `toml.Unmarshal` errors.

Consequences:

- malformed TOML silently becomes partial/default configuration;
- permission and I/O failures look like missing configuration;
- a typo can appear as an unrelated runtime behavior;
- callers cannot explain which file failed.

**Minimal correction:** change the contract to `Load(repoRoot string) (Config, error)`, ignore only `os.ErrNotExist`, and wrap parse/read failures with the file path. Propagate the error through the small number of startup callers. If fallback is desired product behavior, surface an explicit warning rather than silently swallowing the error.

Do not add schema validation machinery unless concrete invalid values require it.

### 2. `publish.BuildPreview` has a hidden persistent side effect

**Location:** `internal/publish/publish.go`, `BuildPreview`

The name and return type suggest assembly of a preview, but the function calls `timeline.SyncCommits`, which can append to `timeline.jsonl`. It discards that error. It also discards errors from reading the conclusion.

This means a dry-run/preview path can mutate local state, and an incomplete preview can be produced without explaining why.

**Minimal correction:** make commit synchronization an explicit caller step, then make preview assembly read-only. At minimum, return synchronization errors and distinguish a missing optional conclusion from other read failures.

Avoid introducing a `PreviewBuilder` interface or service object; two explicit functions are enough.

### 3. Transcript truncation can create invalid UTF-8

**Location:** `internal/transcript/transcript.go`, `Text`

The final tail operation slices the output string at `len(out)-maxBytes`. That offset can land inside a multibyte rune.

**Minimal correction:** advance the cut point to the next valid UTF-8 boundary. Preserve the byte limit; converting the whole transcript to `[]rune` would add unnecessary memory. A focused Japanese-text test is sufficient.

A bounded streaming tail is a separate performance improvement and should not be bundled unless requested.

### 4. Exported comments contain stale or misplaced text

**Locations:**

- `internal/github/github.go`: a `FindPreview` comment appears immediately above `ListOpen`;
- `internal/git/git.go`: the `ShowStat` description appears above `FetchPull`.

These do not affect runtime behavior, but incorrect comments are worse than absent comments because readers trust exported API documentation.

**Minimal correction:** move/delete the misplaced lines. Do not add comments to every helper.

### 5. Remove verified dead/duplicate code

**Locations:**

- `internal/tui/tui.go`: `wrapLines` is unused;
- `internal/tui/tui.go`: `deriveTitle` duplicates `prbody.Title` logic;
- `internal/github/github.go`: `listNode.Comments` and `listNode.Commits` are not populated by the lightweight list query or used while shaping rows.

**Minimal correction:** delete `wrapLines` and unused list fields; replace `deriveTitle` with a file read followed by `prbody.Title`, preserving existing read fallback behavior.

This is small cleanup, not a reason to create a generic text utility package.

## Refactor when touched — main maintainability debt

### 6. `internal/tui/tui.go` combines too many change reasons

**Evidence:**

- 3,011 production lines in one file;
- `Model.Update` begins around line 713 and ends around line 1,458;
- `Model` owns local/remote targets, two screens, tabs, views, filters, stack collapse, cursors, async generations, action confirmation/running state, cache state, formatter cache, PTY state, and viewport state;
- the same file performs Git/GitHub calls, persistence updates, message handling, key handling, layout, and rendering.

The problem is not the raw line count alone. The problem is that a change to one feature often requires understanding multiple unrelated state invariants.

Examples of invariants currently encoded implicitly across branches:

- `pendingPRAction` versus `prActionRunning`;
- `refreshing` versus `listRefreshing` versus `publishing`;
- `screen`, `remote`, `localAvailable`, and current cache/target identity;
- `focusDiff`, `focusExplorer`, active tab, and PTY availability;
- list generation versus target generation;
- `allPRs`, filtered rows, visible rows, stacks, and selected PR number.

**Safe incremental decomposition:** keep one `tui` package and one `Model`, but split cohesive code into files as it is changed:

- `model.go`: types, construction, lifecycle, layout;
- `update.go`: top-level `Update` dispatcher;
- `update_messages.go`: async result handlers;
- `update_keys.go`: screen-specific key handling;
- `pr_list.go`: filters, views, stacks, PR-list rendering;
- `conversation.go`: item derivation and Conversation rendering;
- `detail.go`: file/commit detail identity and rendering orchestration;
- `pr_actions.go`: confirmation and action commands.

File movement alone is not the goal. Each extraction should make one state transition testable and readable without opening the entire file.

**Do not:**

- split every message into a type with a `Handle` method;
- add a controller/service/repository hierarchy;
- create interfaces with one implementation;
- rewrite the Bubble Tea model wholesale;
- move rendering into another package while it still depends heavily on model internals.

### 7. `Model.Update` should become a dispatcher, not a second framework

`Update` currently handles PTY routing, spinner/window messages, every async result, action completion, screen-specific keys, modal confirmation, and viewport fallback.

A minimal target shape is:

1. route PTY-owned messages;
2. route typed async/system messages to focused helpers;
3. route keys by current modal/screen/focus state;
4. pass unhandled messages to the active viewport.

Extract branches that already form cohesive blocks, such as:

- `handlePRListRefreshed`;
- `handlePRPreviewLoaded`;
- `handleRemoteLoaded`;
- `handlePRActionDone`;
- `handlePRListKey` and `handleDetailKey`.

Helpers should return `(Model, tea.Cmd)` or mutate `*Model` consistently within a group. Do not extract one-line branches merely to reduce line count.

### 8. Derived TUI state has multiple parallel representations

**Location:** `internal/tui/tui.go`

PR navigation uses source rows, state-filtered rows, visible rows, stacks, collapse state, and cursor selection. Conversation items are reconstructed from PR description, events, comments, and activities; selection/key restoration repeatedly derives that combined slice.

This is currently correct and tested, but it makes the source of truth difficult to identify.

**Recommendation when these features change:**

- document the source-to-derived flow next to model fields;
- centralize PR derivation in `applyPRFilters` and Conversation derivation in one function;
- compute a derived snapshot once per source-data change if measurements justify it;
- identify selection by PR number/conversation key, not only by derived index.

Do not introduce a general reactive state/store abstraction.

### 9. Some “build” functions perform external I/O

**Locations:**

- `internal/tui/tui.go`, `buildDetail` calls `git.FileDiffRange`;
- `commitDetail` calls `git.Show`;
- `internal/publish`, `BuildPreview` synchronizes/writes timeline state.

Names such as `build*` and `*Preview` normally imply deterministic transformation. Hidden process/file mutation makes call sites harder to reason about and contributed to the repeated-work issue documented in the performance audit.

**Recommendation:** use explicit load/fetch names for I/O and pass resulting data into rendering/assembly helpers. Apply this only at active change points rather than renaming every builder.

### 10. `github.Client.ListState` is cohesive but too dense

**Location:** `internal/github/github.go`, `ListState`

The method validates state, resolves repository identity, defines GraphQL transport types/query, paginates two connections, deduplicates rows, and maps API nodes into domain PRs.

This is not a package-boundary failure: all work belongs to the GitHub adapter. It is a local readability/testability issue.

**When list behavior changes, extract only:**

- repository owner/name resolution;
- one page request/response type;
- node-to-`PR` transformation.

Keep pagination orchestration in `ListState`. Do not create a GraphQL framework or move cache persistence into a new package yet.

### 11. Demo GitHub behavior is readable only as a shell program

**Location:** `cmd/demo_github.go`, `demoGHScript`

The stateful mock is appropriately isolated from production and is valuable for safe demos. Its embedded shell/JSON string is dense and sensitive to quoting.

**Recommendation:** leave it alone unless demo behavior expands. When touched substantially, prefer an internal Go mock executable/subcommand or a checked fixture transformed by a small builder. Extracting the current string solely for aesthetics would add packaging/embed complexity without improving behavior.

## Diagnostics and API quality

### 12. Git and summarizer process errors lose useful diagnostics

**Locations:**

- `internal/git/git.go`, `run`;
- `internal/summarize/summarize.go`, `Claude.Summarize`.

Both use `cmd.Output()`. On failure, callers commonly receive only an exit status unless they inspect `*exec.ExitError`; stderr is not consistently included with operation context.

**Recommendation when the runner is changed:** use `CombinedOutput` or explicitly capture stderr and wrap errors with the command operation, not necessarily every raw argument. Context/cancellation can be added at the same boundary where UI responsiveness needs it.

One small internal Git runner remains preferable to a command interface implemented by every call site.

### 13. Persistent commit identity uses abbreviated SHA

**Locations:** `internal/git.CommitsRange`, `internal/timeline.SyncCommits`

`git log` emits `%h`, and the value is persisted/deduplicated in timeline events. Git abbreviations are usually unique at creation time, so this is not an urgent correctness failure. They can become ambiguous as object sets grow, and abbreviation length can vary by repository configuration.

**Recommendation:** use full `%H` for newly persisted identity when a migration/compatibility plan is available; shorten only at rendering. Do not silently change existing timeline identity in an unrelated cleanup PR.

### 14. Minor names do not justify broad renaming

Workflow reviewers noted `openPRs`, `PRsState`, and a local `wrapper` identifier as imperfect. Cross-validation correctly downgraded them.

- `openPRs` now sometimes represents the currently visible/listed state, including Closed;
- `PRsState` is grammatically awkward;
- `wrapper` is generic.

These are locally understandable and widely referenced. Rename them only while changing the surrounding behavior, with focused compiler-assisted edits. A rename-only campaign would create noise without materially improving design.

## Test quality

### What is good

- tests are colocated with package boundaries;
- failure and stale-result paths are covered, not only happy paths;
- temp Git repositories and command fakes verify real integration behavior;
- publish conflict handling fails closed;
- diff and PTY lifecycle safety is tested;
- CI includes tests, race checks for the most concurrent packages, vet, and diff checks.

### Maintainability concerns

`internal/tui/tui_test.go` is 1,585 lines and necessarily white-box-heavy. Tests frequently construct or mutate internal `Model` fields and assert rendered strings/ANSI output. This gives broad protection but can make safe internal refactoring expensive.

Do not rewrite these tests. As TUI handlers are extracted:

- test state transition inputs/outputs at the handler seam;
- keep a smaller number of rendering integration assertions;
- share model/window fixture setup where duplication is proven;
- prefer stable semantic assertions over full styled output equality.

### Focused missing tests

Add tests alongside the objective fixes for:

- malformed global/repository TOML and unreadable config files;
- preview synchronization/read failure behavior;
- UTF-8 transcript tail truncation;
- `summarize.Parse` heading/title edge cases;
- stderr propagation from failed Git/summarizer processes;
- full-SHA persistence if that format changes.

Do not pursue a coverage percentage target or create tests for trivial getters.

## Directory structure recommendation

No broad directory move is recommended.

In particular:

- keep `cmd/demo*.go` in `cmd` while it remains command fixture setup;
- keep GitHub cache types in `internal/github` until independent reuse or growth creates a real package boundary;
- keep TUI rendering and state transitions in one `internal/tui` package;
- split `tui.go` into files before considering subpackages;
- keep platform-specific PTY/process files beside their package with build tags.

The repository needs finer file-level navigation inside `internal/tui`, not more architectural layers.

## Recommended action order

### Fix independently

1. return config parse/read errors;
2. separate timeline synchronization from preview assembly and stop discarding errors;
3. make transcript truncation UTF-8 safe;
4. correct exported comments and remove verified dead fields/functions;
5. consolidate title derivation through `prbody.Title`.

### Refactor opportunistically

1. turn `Update` into a dispatcher as individual message/key areas change;
2. split `tui.go` into cohesive same-package files;
3. make Git-backed detail loading explicit rather than hidden in builders;
4. extract `ListState` page transport/mapping when GitHub listing changes;
5. improve Git/summarizer error context and cancellation at the command boundary.

### Leave unchanged

- package hierarchy outside `internal/tui`;
- branch-local versus navigator cache separation;
- native Git/GitHub command adapters;
- managed PR body ownership model;
- atomic cache replacement;
- bounded diff/PTY behavior;
- the demo mock until its behavior grows;
- minor naming imperfections outside active work.

## Definition of done for quality refactors

A refactor should meet all of these conditions:

- behavior remains unchanged unless a separately documented defect is fixed;
- the diff reduces the number of responsibilities needed to understand the edited path;
- no single-implementation interface or speculative package is added;
- existing state-generation, cache, and cancellation guarantees remain intact;
- focused tests cover the extracted transition/contract;
- `go test ./...`, `go vet ./...`, and `git diff --check` pass.

The goal is not fewer lines or more files by itself. The goal is for a maintainer to change one feature while reading fewer unrelated states and side effects.
