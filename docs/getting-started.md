# Getting started

## Run with Docker

```sh
docker run --rm -p 4280:4280 ghcr.io/intentius/mudflaps:latest
```

## Run with Go

```sh
go install github.com/intentius/mudflaps/cmd/mudflaps@latest
mudflaps
```

By default mudflaps listens on `:4280`, echoing Fly's internal
`_api.internal:4280`. Change it with `-addr` or the `MUDFLAPS_ADDR` environment
variable:

```sh
mudflaps -addr :8080
# or
MUDFLAPS_ADDR=:8080 mudflaps
```

## Point a client at it

Machines-API clients read a base-URL override from the environment. Set it to
the address mudflaps listens on:

```sh
export FLY_FLAPS_BASE_URL=http://localhost:4280
```

All API paths are prefixed with `/v1`, exactly as against the real service.

## A quick tour with curl

```sh
BASE=http://localhost:4280

# Create an app.
curl -s -X POST "$BASE/v1/apps" \
  -d '{"app_name":"demo","org_slug":"personal"}'

# Create a machine and capture its id.
MID=$(curl -s -X POST "$BASE/v1/apps/demo/machines" \
  -d '{"region":"local","config":{"image":"nginx"}}' | jq -r .id)

# Block until it is running.
curl -s "$BASE/v1/apps/demo/machines/$MID/wait?state=started"
# => {"ok":true}

# Inspect it.
curl -s "$BASE/v1/apps/demo/machines/$MID" | jq '{id, state, instance_id}'
```

## Check what is implemented

```sh
curl -s http://localhost:4280/_mudflaps/health | jq
```

The response reports the running version and lists implemented and roadmap
paths.
