# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Volume endpoints (`GET`/`POST /v1/apps/{app}/volumes`, `GET`/`PUT`/`DELETE
  /v1/apps/{app}/volumes/{vol}`).
- `GET /v1/platform/regions` returns a static, representative list of Fly regions
  (unblocks region validation for clients).

## [0.2.0] - 2026-07-10

Fidelity and correctness pass from an adversarial audit against `superfly/fly-go`.

### Fixed

- `stop` accepts `timeout` as a duration string (`"0s"`/`"10s"`), matching
  fly-go, instead of rejecting every real client's stop body with `400`.
- `skip_launch` machines rest in `created` rather than the transient `creating`.
- Destroyed machines are reaped from the store, and mutating operations on a
  destroying/destroyed machine are rejected (`400`) instead of resurrecting it.
- `cloneMachine` deep-copies nested service ports/handlers; `clampTimeout` clamps
  non-positive/garbage values to the 1s floor; the update response carries a
  fresh `updated_at`.
- App deletion clears its machines' leases; graceful shutdown cancels in-flight
  `/wait` long-polls so it drains promptly and exits cleanly.

### Changed

- `/wait` honors fly-go's `version` filter and a repeatable `state` param (any
  requested state satisfies the wait); `instance_id` still accepted.
- `cordoned` is surfaced on the machine object (`json:"cordoned"`).
- `signal`, `exec`, and `ps` answer an honest `501` and appear in
  `/_mudflaps/health` instead of falling through to a bare `404`.
- Response shapes match flaps: create-machine returns `200`, `start` returns a
  `MachineStartResponse`, and stop/restart/suspend/cordon/delete return an empty
  body; the non-fly-go `description` field is dropped from lease data.
- Documentation site (`api-coverage`, `fidelity`) synced with the implemented
  surface; added a `RELEASING.md` and a `just release` recipe.

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

[Unreleased]: https://github.com/intentius/mudflaps/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/intentius/mudflaps/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/intentius/mudflaps/releases/tag/v0.1.0
