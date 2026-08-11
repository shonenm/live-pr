# OSS release readiness audit

## Scope and method

This report audits the `live-pr` repository at commit `2336bf2` for readiness to announce and accept users as a public OSS alpha.

It covers:

- licensing and release artifacts;
- installation and first-run experience;
- project positioning and documentation;
- contribution and support paths;
- security and repository settings;
- known correctness issues that affect user trust;
- a bounded publication checklist.

This is not a competitor analysis, a request to finish the full roadmap, or a requirement to reach stable `v1.0` quality.

The audit checked the repository contents, GitHub repository metadata, release assets, GitHub Actions history, and the latest `v0.1.0-alpha.6` archive. It also incorporates relevant findings from [code-quality-audit.md](code-quality-audit.md) and [performance-audit.md](performance-audit.md).

Local verification passed:

```text
go test ./...
go vet ./...
git diff --check
```

The latest Darwin arm64 archive passed its published SHA-256 checksum and reported `live-pr version 0.1.0-alpha.6`.

## Executive summary

`live-pr` is technically ready for a **public alpha**, but not yet ready for a broad community announcement.

The implementation already has a stronger foundation than many first OSS releases: an MIT license, automated cross-platform builds, passing CI, checksummed release archives, a safe stateful demo, broad behavioral tests, and explicit protection around managed PR-body updates.

The remaining launch blockers are mostly at the product boundary:

1. the README has no visual proof of the core workflow;
2. the installation example points to an older release and does not clearly separate required and optional dependencies;
3. supported alpha behavior and limitations are not stated compactly;
4. contributors and security reporters have no documented path;
5. malformed configuration and preview-side mutation can undermine user trust;
6. repository protections and dependency-security automation are not enabled.

These are bounded launch tasks. Homebrew, multi-agent adapters, inline review comments, a documentation site, and broad performance work should not delay the first public alpha announcement.

## Current readiness

### Already sufficient

| Area | Evidence | Assessment |
| --- | --- | --- |
| License | Root `LICENSE`, MIT detected by GitHub | Ready |
| Public source | Public GitHub repository | Ready |
| Automated verification | Tests, race tests, vet, module verification, diff check | Ready |
| Platform builds | Linux, macOS, and Windows CI builds | Ready |
| Release automation | Tag-triggered GoReleaser workflow | Ready |
| Binary distribution | macOS/Linux archives, Windows zip, checksums | Ready |
| Release verification | Latest Darwin arm64 archive checksum and version verified | Ready |
| Safe evaluation path | `live-pr demo` uses disposable Git and mocked GitHub state | Ready |
| Runtime-state isolation | XDG state with migration from old repo-local state | Ready |
| PR body ownership | Managed markers and remote-edit conflict detection | Ready |
| Test coverage | Behavioral coverage across config, GitHub, publish, diff, PTY, and TUI state | Ready for alpha |

### Not yet sufficient for broad announcement

| Area | Current state | Required result |
| --- | --- | --- |
| Visual onboarding | No screenshots, GIFs, or recordings in the repository | Core workflow is understandable without installing |
| Quick start | Install example references `v0.1.0-alpha.4`; requirements are implicit | Current, copyable path from install to demo |
| Alpha contract | Limitations are distributed across README and docs | One explicit supported/unsupported section |
| Contribution path | No `CONTRIBUTING.md` or templates | Users know how to report and submit changes |
| Security reporting | No `SECURITY.md`; GitHub reports security policy disabled | Private reporting path and supported-version statement |
| Config failure behavior | Read and TOML parse errors can be silently ignored | Invalid configuration fails or warns with its path |
| Preview semantics | Preview can synchronize/write timeline state and discard errors | Preview is read-only, or mutation is explicit and errors propagate |
| Repository protection | `main` is unprotected | Required CI checks protect direct integration |
| Dependency/security automation | Dependabot security updates and GitHub security analysis are disabled | At least dependency security updates enabled |

GitHub's Community Profile reports 42% completeness. The missing community files are not all release blockers, but contribution and security guidance are the minimum useful additions before soliciting outside users.

## P0 — Complete before community announcement

### 1. Add one visual demonstration of the core workflow

**Problem:** the README describes a visual TUI product entirely in text. A new visitor cannot quickly verify what the product is or how its pieces connect.

**Minimum change:** add one short GIF or compact recording near the README introduction showing:

1. a decision or summary arriving from an agent session;
2. the decision timeline beside the branch diff;
3. the generated GitHub PR body preview.

The recording should demonstrate the product's core loop rather than every PR-list action. Reuse `live-pr demo` so the recording does not create or mutate GitHub resources.

**Done when:** a visitor can understand the main workflow from the first README screen without reading the complete feature list.

### 2. Replace the install section with a current quick start

**Problem:** the README calls `v0.1.0-alpha.4` current while the latest release is `v0.1.0-alpha.6`. It also mixes installation, optional reviewer setup, GitHub publishing, and development commands.

**Minimum change:** provide a short sequence:

```text
install latest alpha
live-pr --version
live-pr demo
```

Then list dependencies by capability:

- required for local use: Git;
- required for GitHub browsing/publishing: authenticated GitHub CLI;
- required only for automatic session summaries: Claude Code CLI;
- optional: Neovim CodeReview or a configured static diff formatter.

Use either a release-page instruction that does not hard-code a stale version or update the pinned version as part of each release process. Keep source installation as a secondary path.

**Done when:** the commands are copyable against the latest release and a new user can reach the mock demo without configuring GitHub or Claude Code hooks.

### 3. State the alpha support contract

**Problem:** important limitations exist, but users must infer them from multiple documents.

**Minimum change:** add a compact `Alpha status` section to the README covering:

- automatic summary hooks currently target Claude Code;
- macOS and Linux support the embedded PTY reviewer;
- Windows uses the raw/static diff fallback;
- GitHub review comments and inline comments are not yet synchronized;
- Homebrew is not yet available;
- the data remains local until the user explicitly publishes a PR.

Do not duplicate the full roadmap. Link to `docs/roadmap.md` for future work.

**Done when:** a user can decide whether their platform and workflow are supported before installing.

### 4. Add minimum contribution and support files

Create:

- `CONTRIBUTING.md` with setup, focused verification commands, PR expectations, and a statement that proposed features should begin as issues;
- `SECURITY.md` with supported versions and a private vulnerability-reporting path;
- a bug-report issue template requesting OS, terminal, `live-pr --version`, reproduction steps, expected/actual behavior, and sanitized logs;
- a feature-request template focused on the workflow problem rather than implementation design.

A pull request template and Code of Conduct are useful but can follow once external contribution begins. Do not add governance documents that have no current process behind them.

**Done when:** a first-time user can report a defect or vulnerability without guessing what information or channel to use.

### 5. Fix configuration error handling

**Source:** [code-quality-audit.md](code-quality-audit.md#1-config-parsing-and-read-errors-are-silently-discarded)

`internal/config.Load` silently ignores malformed TOML and non-not-found read errors. An external user is likely to interpret a typo as broken product behavior.

**Minimum correction:** return or visibly surface read and parse errors with the configuration path, while continuing to ignore genuinely missing optional files.

Do not add schema machinery or a validation framework.

**Done when:** malformed global and repository configuration produces an actionable path-specific error or warning, with focused tests.

### 6. Make preview behavior trustworthy

**Source:** [code-quality-audit.md](code-quality-audit.md#2-publishbuildpreview-has-a-hidden-persistent-side-effect)

`publish.BuildPreview` can synchronize commits into the timeline and discard synchronization/read failures even though preview and dry-run imply observation.

**Minimum correction:** make synchronization an explicit step before preview assembly, keep assembly read-only, and propagate non-optional read/synchronization errors.

**Done when:** `live-pr pr --dry-run` does not unexpectedly mutate persistent state and cannot silently produce a partial preview.

### 7. Protect the default branch and enable dependency security updates

Current GitHub state:

- `main` has no branch protection;
- Dependabot security updates are disabled;
- secret scanning and push protection report disabled.

**Minimum repository settings:**

- require the existing CI workflow before merging to `main`;
- prevent force pushes and branch deletion for `main`;
- enable Dependabot security updates;
- enable available secret scanning and push protection settings where the GitHub plan permits them.

Do not introduce a release approval hierarchy or CODEOWNERS file for a one-maintainer alpha.

**Done when:** an accidental direct integration cannot bypass the checks already defined in CI, and known vulnerable dependency updates have an automated path.

### 8. Run one dependency vulnerability scan

`govulncheck` was not installed during this audit, so Go dependency vulnerability status was not verified.

Run `govulncheck ./...` before the announcement and record or fix reachable findings. Add it to CI only if its signal and runtime are acceptable after the first run; the initial scan does not require a permanent new workflow.

**Done when:** the release commit has a recorded clean scan or documented assessment of each reachable finding.

## P1 — Complete shortly after public alpha

### 9. Validate installation on one clean macOS and one clean Linux environment

CI proves compilation, not first-run usability. Verify the published archives rather than `go run`:

- checksum validation;
- `live-pr --version`;
- `live-pr demo` startup;
- raw/static diff fallback without Neovim;
- GitHub authentication failure produces an actionable message;
- uninstall consists only of removing the binary and optional XDG state.

Windows compilation is already checked. A manual Windows TUI smoke test is useful, but it need not block an alpha explicitly documenting the fallback.

### 10. Improve release notes for user-facing consumption

Current release notes are generated from commit subjects. Keep automation, but add a short manual summary for announced releases:

- what changed for users;
- known limitations;
- upgrade notes if state/config changed;
- link to the demo and issue tracker.

Do not maintain a second changelog until release-note history becomes difficult to navigate.

### 11. Add repository topics and social preview metadata

The repository currently has no topics or custom Open Graph image.

Add a small, accurate set such as:

```text
github pull-request terminal tui ai-agents claude-code code-review golang
```

Use the README demo frame or a simple product screenshot as the social preview. Avoid logo/brand work that delays documentation of the actual product.

### 12. Establish a lightweight feedback loop

Use GitHub Issues as the single support and roadmap channel initially. Add Discussions only when issue traffic shows a real need for open-ended usage questions.

For the first alpha announcement, ask testers to report:

- terminal and platform compatibility;
- setup friction;
- whether the exported timeline improves review;
- which coding-agent adapter they need next.

Do not add product analytics or telemetry for the initial launch. Release downloads, issues, and direct reports are sufficient evidence.

## Do not block the first public alpha on

- Homebrew distribution;
- multi-agent hook adapters;
- GitHub review and inline-comment synchronization;
- a standalone documentation website;
- Code of Conduct or formal governance;
- GitHub Discussions or Discord;
- completion of the full roadmap;
- broad TUI refactoring;
- speculative cache or data-structure optimization;
- benchmark infrastructure beyond a demonstrated performance problem;
- stable API or backward-compatibility guarantees appropriate to `v1.0`.

The code-quality audit's UTF-8 truncation fix and small dead/comment cleanup are good focused improvements, but they do not need to delay announcement if the supported alpha limitations are explicit. The performance audit found optimization candidates, not a demonstrated release-blocking bottleneck.

## Publication sequence

### Gate A — Repository-ready

- [ ] README contains a core-workflow visual
- [ ] Quick start uses the current release and starts with `live-pr demo`
- [ ] Requirements and optional integrations are separated
- [ ] Alpha support and limitations are explicit
- [ ] `CONTRIBUTING.md` exists
- [ ] `SECURITY.md` exists
- [ ] Bug and feature issue templates exist
- [ ] Config errors are actionable
- [ ] Preview/dry-run behavior is read-only and error-aware
- [ ] `main` requires CI
- [ ] Dependabot security updates are enabled
- [ ] `govulncheck ./...` has been assessed

### Gate B — Release-ready

- [ ] `go test ./...` passes
- [ ] `go test -race ./internal/github ./internal/tui` passes
- [ ] `go vet ./...` passes
- [ ] `go mod verify` passes
- [ ] `git diff --check` passes
- [ ] Clean-environment macOS archive smoke test passes
- [ ] Clean-environment Linux archive smoke test passes
- [ ] Release checksums verify
- [ ] Release notes state user-visible changes and known limitations

### Gate C — Announcement-ready

- [ ] Announcement leads with the decision-timeline workflow, not the PR dashboard feature list
- [ ] Demo media uses disposable/mock GitHub state
- [ ] Link points to the quick start, not only the repository root
- [ ] Feedback request names the questions maintainers want answered
- [ ] Maintainer is prepared to triage installation failures and platform reports

## Release decision

Use the following threshold:

- **Public repository and continued dogfooding:** ready now.
- **Targeted public alpha announcement:** ready after Gate A and Gate B.
- **Broad launch beyond early adopters:** wait for at least a small number of independent successful installs and one iteration on their onboarding feedback.

The goal of the first announcement is not to prove stable-product maturity. It is to make the core workflow understandable, installable, reversible, and supportable enough that outside users can produce trustworthy feedback.
