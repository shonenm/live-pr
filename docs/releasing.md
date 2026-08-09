# Releasing

Releases are built by GoReleaser from version tags.

## Prerequisites

- Choose and commit a project license before the first public release.
- Configure the `shonenm/homebrew-tap` repository if Homebrew distribution is enabled.
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

The tag workflow publishes archives and `checksums.txt` to GitHub Releases and updates the Homebrew tap.

## Local verification

Install GoReleaser and run:

```sh
goreleaser release --snapshot --clean
```

Snapshot artifacts are written under `dist/` and are not committed.
