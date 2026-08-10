set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Run the default verification.
default: check

# Run the test suite.
test:
    go test ./...

# Run static analysis.
vet:
    go vet ./...

# Check formatting and whitespace errors.
diff-check:
    git diff --check

# Run all local checks.
check: test vet diff-check

# Build the binary.
build:
    go build -o live-pr .

# Run a disposable demo.
demo mode="git":
    go run . demo {{mode}}

# Create and push a release tag from main.
release version:
    ./scripts/release {{version}}
