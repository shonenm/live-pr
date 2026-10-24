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

The tag workflow publishes archives and `checksums.txt` to GitHub Releases,
then downloads each archive on a matching runner, verifies its checksum, and
executes `--version` and `skill print`. PR CI runs the same smoke commands on
release-equivalent archives for all five targets. These checks do not exercise
an interactive terminal or contact GitHub from the tested binary.

## Local verification

Install GoReleaser and run:

```sh
goreleaser release --snapshot --clean
```

Snapshot artifacts are written under `dist/` and are not committed.

## Executable footprint

Build comparisons must use the same Go version, OS/architecture, `CGO_ENABLED=0`,
`-trimpath`, and `-ldflags='-s -w'`. Measure file bytes and compressed archive
bytes separately; `go tool nm -size` on an unstripped build is only a partial
attribution, not the savings from removing a dependency. CI prints both sizes
for each target without imposing an arbitrary size gate.

```sh
tmp=$(mktemp -d)
GOTOOLCHAIN=go1.26.10 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$tmp/live-pr" .
wc -c < "$tmp/live-pr"
tar -czf "$tmp/live-pr.tar.gz" -C "$tmp" live-pr
wc -c < "$tmp/live-pr.tar.gz"
```

Baseline measured with Go 1.26.10, version string `audit`, after removing the
Windows delete-before-rename fallback. Archives here contain only the binary;
release metadata and accompanying files can change archive bytes.

| Target | Binary bytes | Archive bytes |
| --- | ---: | ---: |
| darwin/arm64 | 21,310,370 | 6,410,835 |
| darwin/amd64 | 22,398,912 | 6,848,404 |
| linux/arm64 | 20,906,146 | 6,160,902 |
| linux/amd64 | 22,053,026 | 6,756,597 |
| windows/amd64 | 22,354,944 | 6,851,863 |

Only the matching Skill markdown is embedded by live-pr, not `assets/demo.gif`.
The old Bubble Tea API is still required by Portalis; Glamour/Goldmark/Chroma
provide the Markdown renderer. Removing these is not a no-cost size cleanup.
Re-measure before replacing them or limiting supported rendering features.
