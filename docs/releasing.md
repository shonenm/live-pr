# Releasing

Releases are built by GoReleaser from version tags.

## Prerequisites

- The project is released under the MIT License.
- Homebrew distribution is a follow-up; the first alpha publishes GitHub Release archives only.
- Ensure the GitHub Actions `GITHUB_TOKEN` can create releases.

## Release

From a clean, up-to-date `main` checkout:

```sh
./scripts/release v0.1.0-alpha.5
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
