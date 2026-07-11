# API coverage

mudflaps implements the subset of flaps that an infrastructure-as-code applier
exercises: apps, machines (full lifecycle), metadata, wait, leases, volumes, and
secrets (apply-only).
Endpoints that are not yet built answer `501 Not Implemented` with a clear JSON
error rather than a misleading success, and they are listed under
`unimplemented` in the `/_mudflaps/health` payload.

## Implemented

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/apps` | List apps. |
| POST | `/v1/apps` | Create an app. |
| GET | `/v1/apps/{app}` | Get an app. |
| DELETE | `/v1/apps/{app}` | Delete an app and its machines. |
| POST | `/v1/apps/{app}/machines` | Create a machine; starts the lifecycle (`skip_launch` rests at `created`). |
| GET | `/v1/apps/{app}/machines` | List machines. |
| GET | `/v1/apps/{app}/machines/{id}` | Get a machine. |
| POST | `/v1/apps/{app}/machines/{id}` | Update a machine; churns `instance_id`. |
| DELETE | `/v1/apps/{app}/machines/{id}` | Destroy a machine (reaped once settled). |
| POST | `/v1/apps/{app}/machines/{id}/start` | Start a stopped or suspended machine. |
| POST | `/v1/apps/{app}/machines/{id}/stop` | Stop a machine (accepts a `StopMachineInput` body). |
| POST | `/v1/apps/{app}/machines/{id}/restart` | Restart a machine (accepts `?force_stop=`). |
| POST | `/v1/apps/{app}/machines/{id}/suspend` | Suspend a machine (resume via start). |
| POST | `/v1/apps/{app}/machines/{id}/cordon` | Cordon a machine. |
| POST | `/v1/apps/{app}/machines/{id}/uncordon` | Uncordon a machine. |
| GET | `/v1/apps/{app}/machines/{id}/wait` | Block until a target state or `408`. |
| GET | `/v1/apps/{app}/machines/{id}/metadata` | Read machine metadata. |
| POST | `/v1/apps/{app}/machines/{id}/metadata/{key}` | Set a metadata key. |
| DELETE | `/v1/apps/{app}/machines/{id}/metadata/{key}` | Delete a metadata key. |
| GET | `/v1/apps/{app}/machines/{id}/lease` | Read the active lease. |
| POST | `/v1/apps/{app}/machines/{id}/lease` | Acquire or refresh a lease. |
| DELETE | `/v1/apps/{app}/machines/{id}/lease` | Release a lease. |
| GET | `/v1/apps/{app}/volumes` | List volumes. |
| POST | `/v1/apps/{app}/volumes` | Create a volume. |
| GET | `/v1/apps/{app}/volumes/{vol}` | Get a volume. |
| PUT | `/v1/apps/{app}/volumes/{vol}` | Update a volume. |
| DELETE | `/v1/apps/{app}/volumes/{vol}` | Delete a volume. |
| GET | `/v1/apps/{app}/secrets` | List secrets (names + digests; never values). |
| GET | `/v1/apps/{app}/secrets/{name}` | Get a secret's metadata (never its value). |
| POST | `/v1/apps/{app}/secrets/{name}` | Set a secret (apply-only; stores a digest). |
| DELETE | `/v1/apps/{app}/secrets/{name}` | Delete a secret. |
| GET | `/v1/platform/regions` | List Fly regions (a static, representative set). |
| GET | `/_mudflaps/health` | Version and coverage report (mudflaps-only). |

## Roadmap (currently `501`)

| Path | Area |
| --- | --- |
| `/v1/apps/{app}/certificates` | Certificates |
| `/v1/apps/{app}/ip_assignments` | IP assignments |
| `/v1/apps/{app}/machines/{id}/signal` | Send a signal |
| `/v1/apps/{app}/machines/{id}/exec` | Exec in a machine |
| `/v1/apps/{app}/machines/{id}/ps` | List processes |

## Wire fidelity

The wire types live in `internal/flaps/types.go`. Their JSON tags are
hand-mirrored against [github.com/superfly/fly-go](https://github.com/superfly/fly-go)
and the [OpenAPI document](https://docs.machines.dev/spec/openapi3.json), which
are the conformance oracles. mudflaps does not import fly-go, so the tags are the
contract: a client must be able to marshal into and unmarshal out of these types
unchanged.
