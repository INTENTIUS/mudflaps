# mudflaps

A standalone, stateful local emulator of the Fly.io Machines API (flaps). Like
LocalStack, but for Fly Machines.

## What it is

mudflaps runs a small HTTP server that speaks the flaps wire protocol over the
`/v1` path space and keeps apps, machines, leases, and version history in
memory. Point a Machines-API client at it by setting `FLY_FLAPS_BASE_URL` and
your client talks to mudflaps instead of `https://api.machines.dev`.

## Why it exists

Testing a Machines-API client, such as an infrastructure-as-code applier, means
testing against state. A machine moves through `creating` to `started`. A lease
that another caller holds blocks your mutation. An update mints a new
`instance_id` and retires the old version. A schema mock such as Prism or
WireMock validates request and response shapes against the OpenAPI document, but
it has no memory, so it cannot model any of that. mudflaps does.

## What it models faithfully

- The machine lifecycle state machine, including transient states.
- Leases: nonce, TTL, owner, expiry, and conflict on mutation by a non-holder.
- Version churn on update.
- The `/wait` long-poll, including timeout behavior.

See [Fidelity](fidelity.md) for the details, and for the boundaries of what
mudflaps deliberately does not do.

## Next steps

- [Getting started](getting-started.md): run it and point a client at it.
- [API coverage](api-coverage.md): which endpoints are implemented.
- [Contributing](contributing.md): how to work on mudflaps.
