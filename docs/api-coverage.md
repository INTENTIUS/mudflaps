# API coverage

mudflaps implements the subset of flaps that an infrastructure-as-code applier
exercises: apps, machines, wait, and leases. Endpoints that are not yet built
answer `501 Not Implemented` with a clear JSON error rather than a misleading
success, and they are listed under `unimplemented` in the
`/_mudflaps/health` payload.

## Implemented

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/apps` | List apps. |
| POST | `/v1/apps` | Create an app. |
| GET | `/v1/apps/{app}` | Get an app. |
| DELETE | `/v1/apps/{app}` | Delete an app and its machines. |
| POST | `/v1/apps/{app}/machines` | Create a machine; starts the lifecycle. |
| GET | `/v1/apps/{app}/machines` | List machines. |
| GET | `/v1/apps/{app}/machines/{id}` | Get a machine. |
| POST | `/v1/apps/{app}/machines/{id}` | Update a machine; churns `instance_id`. |
| DELETE | `/v1/apps/{app}/machines/{id}` | Destroy a machine. |
| POST | `/v1/apps/{app}/machines/{id}/start` | Start a stopped machine. |
| POST | `/v1/apps/{app}/machines/{id}/stop` | Stop a machine. |
| POST | `/v1/apps/{app}/machines/{id}/restart` | Restart a machine. |
| GET | `/v1/apps/{app}/machines/{id}/wait` | Block until a target state or `408`. |
| GET | `/v1/apps/{app}/machines/{id}/lease` | Read the active lease. |
| POST | `/v1/apps/{app}/machines/{id}/lease` | Acquire or refresh a lease. |
| DELETE | `/v1/apps/{app}/machines/{id}/lease` | Release a lease. |
| GET | `/_mudflaps/health` | Version and coverage report (mudflaps-only). |

## Roadmap (currently `501`)

| Path | Area |
| --- | --- |
| `/v1/apps/{app}/volumes` | Volumes |
| `/v1/apps/{app}/machines/{id}/metadata` | Machine metadata |
| `/v1/apps/{app}/secrets` | Secrets |
| `/v1/apps/{app}/certificates` | Certificates |
| `/v1/apps/{app}/ip_assignments` | IP assignments |

## Wire fidelity

The wire types live in `internal/flaps/types.go`. Their JSON tags are
hand-mirrored against [github.com/superfly/fly-go](https://github.com/superfly/fly-go)
and the [OpenAPI document](https://docs.machines.dev/spec/openapi3.json), which
are the conformance oracles. mudflaps does not import fly-go, so the tags are the
contract: a client must be able to marshal into and unmarshal out of these types
unchanged.
