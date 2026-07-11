# Contributing to mudflaps

Thanks for your interest in improving mudflaps. This document covers how to get
set up, the conventions the project follows, and how to submit changes.

## Getting started

You need Go 1.25 or newer. The project uses [`just`](https://github.com/casey/just)
as its task runner (a `Makefile` mirrors the same targets if you prefer `make`).

```sh
git clone https://github.com/intentius/mudflaps
cd mudflaps
just build
just test
```

## Before you open a pull request

Run the same checks CI runs:

```sh
just fmt     # gofmt -w
just lint    # golangci-lint run
just race    # go test -race ./...
```

CI runs `go build`, `go vet`, a `gofmt` check, `golangci-lint`, the race test
suite with coverage, and `mkdocs build --strict`. A green local run of the
commands above should keep CI green.

## Conventions

- The wire types in `internal/flaps/types.go` are the contract. Their JSON tags
  must match the real flaps wire format. github.com/superfly/fly-go is the
  conformance oracle; when in doubt, check it and the OpenAPI document at
  https://docs.machines.dev/spec/openapi3.json.
- Time-dependent behavior (state transitions, lease expiry) goes through the
  `internal/clock` abstraction so tests stay deterministic and fast. Do not call
  `time.Now` or `time.Sleep` directly in the state machine or lease code.
- New behavior needs a test. The store, machine, lease, and server packages all
  have table- and harness-style tests to follow.
- Keep the build self-contained. mudflaps deliberately avoids third-party
  dependencies; prefer the standard library.

## Documentation

Doc pages live in `docs/` and are built with mkdocs-material. Preview them with
`just docs-serve` and validate with `just docs-build` (which runs
`mkdocs build --strict`, the same check CI enforces).

## Reporting bugs and requesting features

Open an issue using the bug or feature template. For anything security-related,
see [SECURITY.md](./SECURITY.md) instead of filing a public issue.

## Releasing

Maintainers: see [RELEASING.md](./RELEASING.md). Releases are tag-driven — `just release X.Y.Z` from a clean `main`.
