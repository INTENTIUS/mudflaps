# mudflaps

A standalone, stateful local emulator of the Fly.io Machines API (flaps). Like LocalStack, but for Fly Machines.

[![CI](https://github.com/intentius/mudflaps/actions/workflows/ci.yml/badge.svg)](https://github.com/intentius/mudflaps/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/intentius/mudflaps)](https://goreportcard.com/report/github.com/intentius/mudflaps)
[![Go Reference](https://pkg.go.dev/badge/github.com/intentius/mudflaps.svg)](https://pkg.go.dev/github.com/intentius/mudflaps)
[![codecov](https://codecov.io/gh/intentius/mudflaps/branch/main/graph/badge.svg)](https://codecov.io/gh/intentius/mudflaps)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Release](https://img.shields.io/github/v/release/intentius/mudflaps)](https://github.com/intentius/mudflaps/releases)
[![GHCR](https://img.shields.io/badge/ghcr.io-intentius%2Fmudflaps-blue)](https://github.com/intentius/mudflaps/pkgs/container/mudflaps)

## Why mudflaps

Testing a Machines-API client, such as an infrastructure-as-code applier, means testing against state: a machine that moves through `creating` to `started`, a lease that another caller holds, an update that mints a new `instance_id` and retires the old version. A schema mock (Prism or WireMock pointed at the OpenAPI spec) validates request and response shapes but has no memory, so it cannot model any of that. No stateful flaps emulator existed, so we built one. mudflaps keeps apps, machines, leases, and version history in memory and advances machines through their lifecycle over short, deterministic delays, giving client tests a real target to converge against.

## Features

- Stateful in-memory store of apps and machines with real CRUD.
- Asynchronous lifecycle state machine: transient states settle into resting states on an injected clock.
- Machine leases with nonce, TTL, owner, expiry, and 409 conflict on mutation by a non-holder.
- `/wait` long-poll that blocks until a target state or times out (408).
- Version churn: an update mints a fresh `instance_id` and marks the prior version `replaced`.
- Honest coverage reporting: a `/_mudflaps/health` endpoint lists implemented and roadmap paths; unimplemented endpoints answer `501` instead of pretending.
- Single static binary, distroless container image, no external dependencies at runtime.

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

Point any Machines-API client at it with the same environment variable the real
client uses:

```sh
export FLY_FLAPS_BASE_URL=http://localhost:4280
```

The listen address is configurable with `-addr` or `MUDFLAPS_ADDR` (default
`:4280`, echoing Fly's internal `_api.internal:4280`).

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

# Take a lease, then see a mutation without the nonce get rejected.
NONCE=$(curl -s -X POST "$BASE/v1/apps/demo/machines/$MID/lease" \
  -d '{"ttl":30}' | jq -r .data.nonce)
curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST "$BASE/v1/apps/demo/machines/$MID/stop"
# => 409
```

## How it compares

| Capability | mudflaps | Prism / WireMock | Real Fly |
| --- | --- | --- | --- |
| State machine (create → started, stop, destroy) | Yes | No | Yes |
| Leases and nonce conflicts | Yes | No | Yes |
| Version churn (new `instance_id`, old `replaced`) | Yes | No | Yes |
| Runs fully offline | Yes | Yes | No |
| Cost | Free | Free | Billed |
| Real microVMs, images, networking | No | No | Yes |

## API coverage

mudflaps implements the apps, machines, wait, and lease endpoints; volumes,
certificates, and IP assignments answer `501` and are listed as roadmap items in
the `/_mudflaps/health` payload. The full table lives in the
[API coverage docs](https://intentius.github.io/mudflaps/api-coverage/).

## Roadmap

mudflaps is the local target for the `chant` fly lexicon's `flyApply` applier.
Tracking issues: [chant #736 (epic)](https://github.com/intentius/chant/issues/736)
and [chant #740](https://github.com/intentius/chant/issues/740). Volumes,
secrets, and certificate endpoints are next.

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
