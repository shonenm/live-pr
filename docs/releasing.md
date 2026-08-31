# Releasing

Releases are built by GoReleaser from version tags.

## Prerequisites

- The project is released under the MIT License.
- Ensure the GitHub Actions `GITHUB_TOKEN` can create releases.
- Homebrew: goreleaser pushes a cask (macOS) to
  [shonenm/homebrew-live-pr](https://github.com/shonenm/homebrew-live-pr) on
  every stable tag (prereleases are skipped). This needs the
  `HOMEBREW_TAP_GITHUB_TOKEN` repository secret — a fine-grained PAT with
  contents read/write on `homebrew-live-pr`. Without it the release job
  fails at the cask publish step. Linux users install from the GitHub
  archive or `go install`.

## Release

Complete the [local-first E2E checklist](local-first-e2e.md) when the release changes review state, polling, or Git integration. Then, from a clean, up-to-date `main` checkout:

```sh
./scripts/release v0.1.0
```

The maintainer-only script verifies the version, branch, clean worktree,
`origin/main` parity, existing tags, `go test ./...`, `go vet ./...`, and
`git diff --check`. It asks for confirmation before creating an annotated tag
and pushing it to `origin`. Use `--yes` for explicit non-interactive approval
or `--dry-run` to run checks without creating a tag.

The tag workflow publishes archives and `checksums.txt` to GitHub Releases.

## Local verification

Install GoReleaser and run:

```sh
goreleaser release --snapshot --clean
```

Snapshot artifacts are written under `dist/` and are not committed.
