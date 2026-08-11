set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Run the default verification.
default: check

# Run the test suite.
test:
    go test ./...

# Run race detection where asynchronous state is concentrated.
race:
    go test -race ./internal/github ./internal/tui

# Run static analysis.
vet:
    go vet ./...

# Verify downloaded modules.
mod-verify:
    go mod verify

# Check formatting and whitespace errors.
diff-check:
    git diff --check

# Run all local checks.
check: test race vet mod-verify diff-check

# Build the binary.
build:
    go build -o live-pr .

# Run a disposable demo.
demo mode="git":
    go run . demo {{mode}}

# Create and push a release tag from main.
release version:
    ./scripts/release {{version}}
