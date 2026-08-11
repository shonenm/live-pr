# Test and CI audit

## Scope and method

This report audits the full `live-pr` repository at commit `2336bf2` for:

- existing test coverage and quality;
- correctness-critical paths without focused tests;
- command and integration-test seams;
- Unix/Windows build-tag behavior;
- GitHub Actions and release verification;
- the smallest necessary and sufficient test/CI improvements.

A `codebase-audit` workflow ran 12 agents across all production Go files, all `*_test.go` files, command wiring, scripts, `justfile`, GitHub Actions, release configuration, and relevant docs. A cross-validation pass rejected duplicate and speculative recommendations.

This is an audit and implementation plan only. No tests, production code, or CI configuration were changed as part of this report.

## Executive summary

The premise that tests are entirely missing is incorrect.

Current evidence:

- 18 `*_test.go` files;
- approximately 146 `Test*` functions;
- approximately 73.7% repository-wide statement coverage in the workflow run;
- `go test ./...` passes;
- targeted race tests pass for `internal/github` and `internal/tui`;
- `go vet ./...`, `go mod verify`, and the existing whitespace command pass;
- CI builds Linux/amd64, macOS/arm64, and Windows/amd64.

The core behavior is already tested more thoroughly than the repository size initially suggests. In particular, GitHub operations, publish conflict protection, PR-list/TUI state transitions, diff safety, PTY lifecycle, and cache behavior have substantial coverage.

The real gaps are narrow:

1. `internal/summarize` has no tests;
2. malformed config behavior is untested because errors are currently discarded;
3. transcript tail truncation lacks a UTF-8 boundary test;
4. publish preview side effects and error behavior lack an explicit contract test;
5. review-request GraphQL pagination is not directly tested;
6. CLI command registration/wiring has little direct coverage;
7. Windows fallback code is compiled but not executed in CI;
8. CI's plain `git diff --check` does not meaningfully inspect committed PR changes on a clean checkout.

The correct next step is not a broad test-writing campaign or a coverage target. It is a small set of focused contract tests plus one narrow CI correction.

## Current CI assessment

### Existing CI workflow

`.github/workflows/ci.yml` already provides:

- pull-request and `main` push triggers;
- read-only repository permissions;
- Go version sourced from `go.mod`;
- Go module/build cache;
- `go test ./...` on Linux;
- `go test -race ./internal/github ./internal/tui`;
- `go vet ./...`;
- `go mod verify`;
- Linux/amd64 build;
- macOS/arm64 build;
- Windows/amd64 build.

This is a sound baseline. It covers the main runtime platform, the most stateful/concurrent packages, static analysis, module integrity, and all supported build targets.

### Existing release verification

The release path is also reasonably protected:

- `scripts/release` checks branch, worktree cleanliness, `origin/main`, tags, tests, vet, and diff cleanliness before tagging;
- `.goreleaser.yaml` runs `go mod verify` and `go test ./...` before release builds;
- GoReleaser builds the declared OS/architecture matrix and creates checksums.

There is no need to duplicate the full CI suite again inside the release workflow if tags continue to be created from verified `main` through `scripts/release`.

### Actual CI gaps

#### 1. Windows fallback behavior is compile-only

Windows-specific files include:

- `internal/diffview/process_windows.go`;
- `internal/embeddedterm/terminal_windows.go`.

The build matrix proves that these files compile, but no Windows test executes their fallback contracts. The most important behavior is that embedded CodeReview is reported unavailable while static diff remains usable.

#### 2. The whitespace check does not inspect committed changes

CI currently runs:

```sh
git diff --check
```

On a freshly checked-out, unmodified CI worktree, this compares the working tree/index and normally has nothing to inspect. It does not reliably validate whitespace introduced by the PR's committed diff.

For pull requests, the meaningful check is equivalent to:

```sh
git diff --check "$BASE_SHA...HEAD"
```

For direct pushes, check the pushed commit range or at least the new commit. This should be corrected before adding unrelated CI jobs.

#### 3. Local `just check` does not match CI

`just check` currently runs:

- tests;
- vet;
- working-tree diff check.

It does not run:

- `go mod verify`;
- the targeted race tests.

This is not a CI failure, but maintainers can receive a green local result that is weaker than CI. Add explicit `mod-verify` and `race` recipes if local parity is desired.

## Package-by-package test map

Coverage percentages are snapshots from the workflow run and should be treated as directional, not as acceptance thresholds.

| Package/area | Current test status | Assessment |
|---|---|---|
| root `main` | 0% / no meaningful direct test | Acceptable; trivial entrypoint |
| `cmd` | Demo helpers tested; command wiring mostly indirect | Add one small registration/wiring smoke test |
| `internal/config` | Defaults, legacy path, global/repo overlay tested; ~84% | Strong normal paths; malformed/read failures missing |
| `internal/diffview` | Render, width env, failure, timeout, bounded buffers/output; ~82% | Strong; Windows process helper compile-only |
| `internal/embeddedterm` | Unix lifecycle/message routing; ~67% | Strong Unix behavior; Windows fallback unexecuted |
| `internal/event` | Append/load/missing file; ~82% | Sufficient |
| `internal/git` | Pull-ref safety, changed files, stats, base resolution; ~68% | Core behavior covered; diagnostics/unhappy paths thinner |
| `internal/github` | Client actions, list/preview, cache, navigator state; ~82% | Strong; review-search pagination gap |
| `internal/hook` | Throttle, empty input, append behavior | Sufficient domain behavior; command stdin wiring indirect |
| `internal/markdown` | Rendering, style, cache; ~94% | Sufficient |
| `internal/prbody` | Title, render, merge, conflict, hash; ~86% | Strong and correctness-focused |
| `internal/publish` | Create/update/cache/conflict/push failures; ~74% | Strong remote safety; preview contract gap |
| `internal/review` | Command construction/basic behavior | Sufficient for current scope |
| `internal/store` | Branch paths and migration; ~44% | Lower percentage but core ownership paths covered |
| `internal/summarize` | No tests; 0% | Highest-value pure unit-test gap |
| `internal/theme` | No tests | Acceptable; constants only |
| `internal/timeline` | Idempotent commit synchronization | Sufficient baseline |
| `internal/transcript` | Extraction, tool filtering, byte cap | Add UTF-8 boundary contract |
| `internal/tui` | Broad state/action/render coverage; ~77% | Strong but white-box-heavy; do not expand indiscriminately |

## Strong existing tests to preserve

### GitHub and cache contracts

Existing tests verify:

- explicit GitHub CLI arguments;
- lightweight list fields versus lazy preview fields;
- open PR lookup and operational-error distinction;
- comments and activity decoding;
- merge/checkout/close command behavior;
- cache corruption/version handling;
- Open/Closed navigator-state persistence;
- stale async result rejection.

These tests protect real boundaries and should remain behavior-oriented. Avoid making them more dependent on exact full GraphQL query formatting.

### Publish safety

Existing publish tests cover:

- create versus update;
- push-before-remote-mutation ordering;
- push failure;
- remote mutation failure;
- managed-body conflict fail-closed behavior;
- cache update after successful publish;
- base-branch consistency.

This is one of the strongest areas of the suite. Additional tests should focus on the preview contract rather than duplicating create/update cases.

### TUI behavior

`internal/tui/tui_test.go` covers many important transitions:

- PR list and detail screens;
- filters, views, stacks, selection retention;
- lazy previews and stale generations;
- local versus remote targets;
- merge/checkout/close confirmations;
- focus and navigation;
- diff fallback and formatter completion;
- Conversation composition;
- PTY/reviewer lifecycle.

The risk here is not missing test quantity. It is maintenance cost from tests that directly manipulate model internals and assert detailed rendered output.

### Process safety

`internal/diffview` and `internal/embeddedterm` test the failure-prone process boundaries rather than only helper functions. Timeout, output bounds, exit handling, and lifecycle behavior are high-value tests and should not be replaced with mocks.

## Necessary additions

### P0 — Add now

#### 1. Test `internal/summarize.Parse`

**New file:** `internal/summarize/summarize_test.go`

Minimum table-driven cases:

- leading blank lines are skipped;
- one Markdown heading prefix is accepted;
- first meaningful line becomes the title;
- remaining bullet lines remain the body;
- blank input returns an empty summary;
- Japanese/multibyte title and body remain unchanged.

A separate fake-`claude` command test is useful only if process error diagnostics are changed. Pure parsing tests provide most of the immediate value.

#### 2. Test and fix UTF-8-safe transcript truncation

**Files:**

- `internal/transcript/transcript_test.go`;
- `internal/transcript/transcript.go`.

Add one focused test using multibyte Japanese content where `maxBytes` lands inside a rune. Assert:

- output is valid UTF-8;
- output remains within the byte limit;
- newest content is retained.

This test should accompany the minimal production fix that advances the cut point to a valid UTF-8 boundary.

#### 3. Define and test malformed config behavior

**Files:**

- `internal/config/config_test.go`;
- `internal/config/config.go`;
- small caller updates if `Load` begins returning an error.

Required scenarios:

- missing global/repository files remain valid and use defaults;
- malformed global TOML returns an error naming the file;
- malformed repository TOML returns an error naming the file;
- non-`NotExist` read errors are not silently treated as missing.

The implementation decision should be explicit: fail startup or surface a warning and use defaults. The current silent fallback must not be preserved accidentally by a test.

Do not add a schema-validation dependency.

#### 4. Test review-request pagination independently

**File:** `internal/github/github_test.go`

Add one test where:

- the PR connection finishes on the first request;
- `reviewRequested.pageInfo.hasNextPage` is true;
- the second request contains `reviewAfter` but not a PR cursor;
- requested PR numbers from both review pages are retained;
- PR rows are not duplicated while only the review connection advances.

This covers a real two-cursor loop that the existing PR-pagination test does not exercise.

#### 5. Execute the Windows fallback contract in CI

**New file:** `internal/embeddedterm/terminal_windows_test.go` with `//go:build windows`.

Minimum assertions:

- an empty command returns `nil`;
- a configured command produces a terminal object;
- `Available()` is false;
- `Err()` reports unsupported embedded review;
- `Init()` returns a state message owned by that terminal;
- `Handles()` rejects another session.

**CI change:** after each platform build, run the smallest platform-bound package test needed to execute the implementation, for example:

```sh
go test ./internal/embeddedterm
```

This avoids attempting the full shell-heavy suite on Windows while proving the fallback behavior.

#### 6. Make the CI whitespace check inspect the PR diff

Replace the no-op-on-clean-checkout form with an event-aware committed-diff check.

For pull requests:

```sh
git diff --check "${{ github.event.pull_request.base.sha }}...HEAD"
```

For pushes, use the event's before/after range when available, handling the all-zero initial SHA. Keep local `just diff-check` as the working-tree check; these commands intentionally validate different states.

### P1 — Add with the related behavior change

#### 7. Make the publish-preview contract explicit

**Files:**

- `internal/publish/publish_test.go`;
- likely `internal/publish/publish.go` and timeline helpers.

Current `BuildPreview` synchronizes commits into `timeline.jsonl` and ignores the synchronization error. Therefore `live-pr pr --dry-run` is not read-only.

Before adding a test, choose the contract:

- recommended: preview includes current commits without persisting them;
- alternative: dry-run may synchronize local state, documented explicitly.

Recommended tests:

- preview includes unsynchronized commits;
- preview leaves timeline bytes unchanged;
- commit enumeration failure is returned;
- non-missing conclusion read failure is returned;
- missing optional conclusion behavior is explicit.

This requires a small production design change, so it should not be smuggled into a test-only PR.

#### 8. Add one CLI registration smoke test

**New file:** `cmd/root_test.go`

Verify only stable public wiring:

- expected top-level commands are registered;
- `pr` exposes `--base`, `--draft`, `--dry-run`, and `--force-managed-body`;
- `hook stop` is reachable;
- root version flag is available.

Do not test every Cobra help string. Do not execute the TUI from this smoke test.

A deeper `pr --dry-run` command test should wait until command output uses Cobra's `OutOrStdout` rather than direct `fmt.Printf`, or should be covered through the publish contract instead.

#### 9. Improve process-error tests when diagnostics change

**Files:**

- `internal/git/git_test.go`;
- `internal/summarize/summarize_test.go`.

When Git and `claude` execution begin preserving stderr/context, add one fake-command failure test per adapter. Assert the operation and actionable stderr, not the exact full command line.

Do not add a command-runner interface only for tests. PATH-based fake executables are already an established repository pattern.

### P2 — Later, only when affected code changes

#### 10. TUI handler tests after decomposition

When `Model.Update` is split into focused handlers, add state-transition tests at those handler seams and reduce duplicated setup. Do not add more full-string rendering tests merely to increase coverage.

#### 11. Full commit SHA persistence test

If timeline identity changes from abbreviated `%h` to full `%H`, update `internal/timeline/sync_test.go` to assert full object IDs and add migration/compatibility behavior. This should accompany the format change, not precede it.

#### 12. Store/path edge cases

Only add `Discover`, `HasData`, and OS state-root tests when those behaviors change. Their lower percentage does not currently indicate a high-risk untested algorithm.

## Tests not to add

### No repository-wide coverage threshold

A global threshold would reward tests for trivial wrappers/constants and discourage cleanup that changes line counts. Track coverage as information, not a gate.

If a gate is ever needed, use targeted package or changed-code expectations for correctness-critical packages rather than one repository number.

### No full multi-OS test matrix

Linux already executes the Unix behavior. macOS adds limited behavioral diversity for the cost, while Windows contains shell-incompatible integration tests. Keep native builds for all release targets and add only the focused Windows fallback execution.

### No repository-wide race job by default

The current targeted race packages contain the highest concentration of asynchronous state and shared caches. Expanding `-race ./...` increases CI time while adding little value to packages that have no concurrency.

Add another package only when it gains concurrency.

### No new testing framework

The standard `testing` package, temporary directories, real Git repositories, and PATH-injected fake commands already cover the project's needs. Do not add assertion, mocking, snapshot, or container-test dependencies.

### No tests for deleted code or corrected comments

Unused helpers, unused response fields, and stale exported comments should be removed/corrected directly. Runtime tests are not the right guard for dead-code cleanup.

### No duplicate happy-path TUI snapshots

The TUI suite already has extensive rendered-output assertions. Additional snapshots would make refactoring harder without improving contract coverage.

## Test quality improvements

### Keep behavior-oriented assertions

Prefer assertions such as:

- selected PR number is preserved;
- stale generation does not update state;
- remote mutation did not run after conflict;
- fallback raw diff remains visible;
- cached Open/Closed states coexist.

Avoid asserting incidental full ANSI strings, exact style-construction details, or the complete GraphQL query unless those details are the contract.

### Consolidate only when touched

Some TUI focus/navigation tests repeat setup and differ by one state. Table-driven consolidation may help, but a broad test rewrite would be risky and produce no user-visible improvement. Consolidate alongside the related production refactor.

### Keep real subprocess tests bounded

Real Git/PTY/formatter tests are valuable. Continue to use:

- `t.TempDir`;
- explicit cleanup;
- short operation-specific timeouts;
- local repositories rather than network resources;
- PATH-injected fake commands for GitHub/LLM adapters.

Avoid arbitrary sleeps where a message/channel/process state can be awaited directly.

## Proposed CI shape

A necessary and sufficient CI configuration is:

### Linux quality job

```sh
go test ./...
go test -race ./internal/github ./internal/tui
go vet ./...
go mod verify
git diff --check "$BASE_SHA...HEAD" # event-aware range
```

### Native release-target matrix

- Linux/amd64: `go build ./...`
- macOS/arm64: `go build ./...`
- Windows/amd64: `go build ./...`
- each platform: `go test ./internal/embeddedterm` after platform-specific tests exist

The matrix should remain small. GoReleaser already covers the wider artifact architecture matrix.

### Local parity

Recommended `justfile` recipes:

```make
race:
    go test -race ./internal/github ./internal/tui

mod-verify:
    go mod verify

check: test race vet mod-verify diff-check
```

The local diff check remains a working-tree check. CI must separately check committed ranges.

## Recommended implementation sequence

### Test/CI PR 1 — pure coverage gaps

1. add `internal/summarize` parsing tests;
2. add GitHub review-cursor pagination test;
3. add CLI registration smoke test;
4. add Windows embedded-terminal fallback test;
5. execute the fallback package in the build matrix;
6. fix committed-diff whitespace checking;
7. align `just check` with CI if the additional local runtime is acceptable.

These changes should not alter product behavior.

### Correctness PR 2 — tests plus minimal fixes

1. define config error behavior and add malformed/read-error tests;
2. make transcript truncation UTF-8 safe and add its regression test;
3. preserve actionable Git/summarizer stderr if included in scope.

These are behavior corrections and should be reviewed as such.

### Publish contract PR 3

1. settle whether dry-run must be read-only;
2. separate commit collection from timeline persistence;
3. add preview side-effect/error tests;
4. update docs for the chosen behavior.

Keeping this separate avoids hiding a persistence-semantic change inside general test work.

## Acceptance criteria

The test/CI work is complete when:

- all current tests still pass;
- the focused missing contracts above have tests;
- Windows fallback code executes in CI at least once;
- CI whitespace validation checks committed changes rather than an empty working tree;
- no coverage threshold or new test dependency is introduced;
- no duplicate broad TUI snapshots are added;
- local and CI commands are documented and intentionally aligned;
- behavior-changing fixes are separated from test-only improvements.

## Final assessment

`live-pr` does not need a test suite built from scratch. It already has a solid behavioral suite and a reasonable CI pipeline. The highest return comes from closing six precise gaps and correcting one ineffective CI check, while resisting broad coverage, race, matrix, and snapshot expansion.
