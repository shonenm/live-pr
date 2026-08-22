set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Run the default verification.
default: check

# Run the test suite.
test:
    go test ./...

# Run race detection where asynchronous state is concentrated.
race:
    go test -race ./internal/github ./internal/tui

# Check gofmt formatting.
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted=$(gofmt -l .)
    if [[ -n "$unformatted" ]]; then
        echo "These files are not gofmt-clean; run 'gofmt -w' on them:" >&2
        echo "$unformatted" >&2
        exit 1
    fi

# Run static analysis (govet runs inside golangci-lint).
lint:
    # Pinned to the version CI uses, and run through `go run` so a contributor
    # needs nothing installed beyond Go: same config, same findings, no setup.
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1 run ./...

# Scan for known vulnerabilities in reachable code.
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Verify downloaded modules.
mod-verify:
    go mod verify

# Check formatting and whitespace errors.
diff-check:
    git diff --check

# Run all local checks.
check: test race fmt-check lint vuln mod-verify diff-check

# Build the binary.
build:
    go build -o live-pr .

# Run a disposable demo.
demo mode="git":
    go run . demo {{mode}}

# Create and push a release tag from main.
release version:
    ./scripts/release {{version}}
