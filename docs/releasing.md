# Releasing

Releases are built by GoReleaser from version tags.

## Prerequisites

- The project is released under the MIT License.
- Homebrew distribution is a follow-up; the first alpha publishes GitHub Release archives only.
- Ensure the GitHub Actions `GITHUB_TOKEN` can create releases.

## Release

```sh
go test ./...
go vet ./...
git diff --check

git switch main
git pull --ff-only origin main
git tag -a v0.1.0-alpha.1 -m 'release: v0.1.0-alpha.1'
git push origin v0.1.0-alpha.1
```

The tag workflow publishes archives and `checksums.txt` to GitHub Releases.

## Local verification

Install GoReleaser and run:

```sh
goreleaser release --snapshot --clean
```

Snapshot artifacts are written under `dist/` and are not committed.
