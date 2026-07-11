# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-07-10

### Added

- Stateful in-memory store of apps and machines with full CRUD.
- Asynchronous machine lifecycle state machine driven by an injected clock:
  create → `creating` → `starting` → `started`, plus stop, restart, destroy, and
  suspend/resume (`suspending` → `suspended`, resume via start) transitions.
- Synchronous version churn on update: the update response carries the new
  `instance_id` and the prior version is marked `replaced` (only the boot is
  async), matching flaps.
- Machine leases with nonce, TTL, owner, and expiry; mutations by a non-holder
  are rejected with `409` while a lease is held. A conflicting acquire returns a
  `MachineLease` envelope with the holder's owner/expiry (never the nonce).
- Cordon/uncordon endpoints, and `stop`/`restart` honor their request inputs
  (signal/timeout, `force_stop`).
- Machine metadata endpoints (`GET`/`POST`/`DELETE .../metadata[/{key}]`) — the
  ownership-marker surface.
- `/wait` long-poll endpoint that blocks until a target state or returns `408`
  on timeout (clamped to [1s, 60s]); the deadline is measured on the injected
  clock, so timeouts are deterministic in tests.
- HTTP server covering apps, machines, wait, lease, and metadata endpoints using
  the standard library router.
- `/_mudflaps/health` endpoint reporting version and implemented vs.
  unimplemented paths; volumes, secrets, certificates, and IP assignments return
  `501` (on the Breadth roadmap).
- `mudflaps` command with `-addr` / `MUDFLAPS_ADDR` configuration, structured
  logging, and graceful shutdown.
- Distroless container image, GoReleaser configuration, mkdocs-material doc site,
  and CI.

[Unreleased]: https://github.com/intentius/mudflaps/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/intentius/mudflaps/releases/tag/v0.1.0
