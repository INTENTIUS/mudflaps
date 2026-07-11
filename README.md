# mudflaps

A standalone, stateful local emulator of the Fly.io Machines API (flaps). Like LocalStack, but for Fly Machines.

[![CI](https://github.com/intentius/mudflaps/actions/workflows/ci.yml/badge.svg)](https://github.com/intentius/mudflaps/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/intentius/mudflaps.svg)](https://pkg.go.dev/github.com/intentius/mudflaps)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Release](https://img.shields.io/github/v/release/intentius/mudflaps)](https://github.com/intentius/mudflaps/releases)
[![GHCR](https://img.shields.io/badge/ghcr.io-intentius%2Fmudflaps-blue)](https://github.com/intentius/mudflaps/pkgs/container/mudflaps)

## Purpose

Testing a Machines-API client, such as an infrastructure-as-code applier, requires testing against state: a machine that moves through `creating` to `started`, a lease held by another caller, an update that mints a new `instance_id` and retires the prior version. A schema mock (Prism or WireMock pointed at the OpenAPI spec) validates request and response shapes but holds no state, so it cannot model these behaviors. mudflaps maintains apps, machines, leases, and version history in memory and advances machines through their lifecycle over short, deterministic delays, providing a stateful target for client test suites.

`superfly/fly-go` is the wire-shape reference; JSON field parity with it is the fidelity contract.

## Features

- Stateful in-memory store of apps and machines with full CRUD.
- Asynchronous lifecycle state machine on an injected clock: transient states (`creating`, `starting`, `stopping`, `restarting`, `suspending`, `replacing`, `destroying`) settle into resting states.
- Suspend/resume, cordon/uncordon, and machine metadata endpoints.
- Machine leases with nonce, TTL, owner, and expiry; a mutation by a non-holder returns `409`, and a conflicting acquire returns the holder's lease envelope (without the nonce).
- Synchronous version churn: an update mints a new `instance_id`, marks the prior version `replaced`, and returns the new version in the response.
- `/wait` long-poll that blocks until a target state or returns `408`; the deadline is measured on the injected clock, so timeouts are deterministic in tests.
- A `/_mudflaps/health` endpoint that lists implemented and roadmap paths; unimplemented endpoints return `501`.
- Single static binary and distroless container image; no runtime dependencies.

## Quick start

Run the container:

```sh
docker run --rm -p 4280:4280 ghcr.io/intentius/mudflaps:latest
```

Or install with Go:

```sh
go install github.com/intentius/mudflaps/cmd/mudflaps@latest
mudflaps            # listens on :4280 by default
```

Point a Machines-API client at it with the same environment variable the real
client uses:

```sh
export FLY_FLAPS_BASE_URL=http://localhost:4280
```

The listen address is configurable with `-addr` or `MUDFLAPS_ADDR` (default
`:4280`, matching Fly's internal `_api.internal:4280`).

## Usage example

```sh
BASE=http://localhost:4280

# Create an app.
curl -s -X POST "$BASE/v1/apps" \
  -d '{"app_name":"demo","org_slug":"personal"}'

# Create a machine.
MID=$(curl -s -X POST "$BASE/v1/apps/demo/machines" \
  -d '{"region":"local","config":{"image":"nginx"}}' | jq -r .id)

# Block until it reaches "started".
curl -s "$BASE/v1/apps/demo/machines/$MID/wait?state=started"
# => {"ok":true}

# Take a lease, then observe a mutation without the nonce being rejected.
curl -s -X POST "$BASE/v1/apps/demo/machines/$MID/lease" -d '{"ttl":30}'
curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST "$BASE/v1/apps/demo/machines/$MID/stop"
# => 409
```

## Comparison

| Capability | mudflaps | Prism / WireMock | Real Fly |
| --- | --- | --- | --- |
| State machine (create → started, stop, suspend, destroy) | Yes | No | Yes |
| Leases and nonce conflicts | Yes | No | Yes |
| Version churn (new `instance_id`, old `replaced`) | Yes | No | Yes |
| Machine metadata (ownership markers) | Yes | No | Yes |
| Runs fully offline | Yes | Yes | No |
| Cost | Free | Free | Billed |
| Real microVMs, images, networking | No | No | Yes |

## API coverage

Implemented: the apps and machines endpoints (including start, stop, restart,
suspend, cordon/uncordon, and metadata), `/wait`, and the lease endpoints.
Volumes, secrets, certificates, and IP assignments return `501` and are listed
as roadmap items in the `/_mudflaps/health` payload. The full table is in the
[API coverage docs](https://intentius.github.io/mudflaps/api-coverage/).

## Roadmap

mudflaps is the local target for the `chant` fly lexicon's `flyApply` applier.
Tracking issues: [chant #736 (epic)](https://github.com/intentius/chant/issues/736)
and [chant #740](https://github.com/intentius/chant/issues/740). Volumes,
secrets, certificate, and IP endpoints are tracked under the Breadth milestone.

## Development

The primary task runner is [`just`](https://github.com/casey/just); a `Makefile`
mirrors the same targets.

```sh
just build      # compile
just test       # go test ./...
just race       # go test -race ./...
just lint       # golangci-lint
just cover      # coverage profile
just docs-serve # preview the doc site
```

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](./CONTRIBUTING.md) and the
[Code of Conduct](./CODE_OF_CONDUCT.md).

## License

Licensed under the [MIT License](./LICENSE). Copyright (c) 2026 Intentius.
