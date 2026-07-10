# justfile is the primary task runner for mudflaps (the intentius org uses just).
# The Makefile mirrors these recipes; keep the two in sync.

binary  := "mudflaps"
pkg     := "./..."
image   := "ghcr.io/intentius/mudflaps"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-s -w -X main.version=" + version

# List available recipes.
default:
    @just --list

# Compile the binary.
build:
    go build -ldflags "{{ldflags}}" -o {{binary}} ./cmd/mudflaps

# Run the test suite.
test:
    go test {{pkg}}

# Run the test suite with the race detector.
race:
    go test -race {{pkg}}

# Lint with golangci-lint (matches CI).
lint:
    golangci-lint run {{pkg}}

# Produce a coverage profile and print the total.
cover:
    go test -race -coverprofile=coverage.out {{pkg}}
    go tool cover -func=coverage.out | tail -1

# Tidy module dependencies.
tidy:
    go mod tidy

# Format all Go sources.
fmt:
    gofmt -w .

# Build the container image.
docker:
    docker build -t {{image}}:{{version}} .

# Build and run the server locally.
run: build
    ./{{binary}}

# Build the documentation site with strict checking (matches CI).
docs-build:
    mkdocs build --strict

# Serve the documentation site locally with live reload.
docs-serve:
    mkdocs serve
