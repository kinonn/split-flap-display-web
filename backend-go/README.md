# Split-Flap Display Web — Go Backend

A Go (Fiber) implementation of the backend described in
[`BACKEND_SPEC.md`](../BACKEND_SPEC.md). It exposes the same HTTP/SSE API
as the previous Python implementation, but ships as a single static binary
and consumes a fraction of the memory.

## Layout

```
backend-go/
├── cmd/server/main.go          # entry point: lifecycle, signal handling
├── internal/
│   ├── config/                 # app.conf + env loader (with defaults)
│   ├── models/                 # Message, Priority, HistoryEntry, DTOs
│   ├── store/                  # thread-safe in-memory message store
│   ├── queue/                  # bounded drop-oldest pub/sub queue
│   ├── mqttclient/             # paho wrapper: connect/publish/subscribe
│   ├── scheduler/              # tick loop, idle handling, SSE subs
│   └── api/                    # Fiber routes + SSE handler
├── app.conf                    # default configuration file
├── app.conf.example            # annotated example
├── Dockerfile                  # production image
├── .dockerignore
└── go.mod / go.sum
```

## Requirements

* Go 1.22 or newer (uses `sync/atomic.Bool`, generics-free)
* A reachable MQTT broker (only needed at runtime)
* The static frontend lives at `../frontend/static` when running locally,
  and at `/app/frontend/static` inside the Docker image.

## Configuration

Settings are resolved in the following order (later wins):

1. Built-in defaults (see `internal/config/defaults()`).
2. `app.conf` located in the working directory.
3. Environment variables (same names as the keys).

All keys, types and defaults are documented in
[`BACKEND_SPEC.md §2.2`](../BACKEND_SPEC.md#2-configuration).

## Local development

From the `backend-go/` directory:

```bash
go mod tidy
go run ./cmd/server
```

The server starts on `http://localhost:8100`. Logs every resolved config
value at startup. Point `MQTT_BROKER_HOST`/`MQTT_BROKER_PORT` at your broker
either via the environment or by editing `app.conf`.

## Build

```bash
# Native (default platform):
go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

# Linux static binary (for containers):
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server
```

## Test

```bash
go vet ./...
go test ./... -timeout 30s
```

The unit tests cover config parsing, scheduler selection rules,
validation, queue snapshots, history, wakeup behaviour and Start/Stop
lifecycle. The MQTT client is stubbed in tests — no live broker is
required.

## Run

You can run from the `backend-go/` directory directly:

```bash
./bin/server
```

The binary expects to find `app.conf` in the *current working directory*
and `frontend/static/` relative to it. So the typical invocation from a
checkout is:

```bash
cd backend-go
./bin/server
```

## Docker / production deployment

The production image is a scratch-style static binary on `alpine:3.20`,
around 20 MB. It is built from the **repository root** (not the
`backend-go/` directory) because it bundles `frontend/static` alongside
the binary.

```bash
# Build the image from the repo root:
docker build -f backend-go/Dockerfile -t split-flap-web:latest .

# Run it (mount your broker host via env if necessary):
docker run --rm -p 8100:8100 \
    -e MQTT_BROKER_HOST=mqtt.local \
    split-flap-web:latest
```

Or via `docker-compose` from the repository root:

```bash
docker compose up --build
```

The image:

* Runs as a non-root user (`app`).
* Exposes port `8100`.
* Includes a `wget`-based `HEALTHCHECK` against `GET /api/config`.
* Reads `app.conf` from `/app/app.conf`. Environment variables override
  file values at runtime.

## Environment variables (override `app.conf`)

```
MQTT_BROKER_HOST
MQTT_BROKER_PORT
MQTT_CLIENT_ID
PUBLISH_TOPIC
SUBSCRIBE_TOPIC
DEFAULT_DISPLAY_DURATION
DEFAULT_TARGET_DISPLAY_COUNT
IDLE_MESSAGE
IDLE_MODE
IDLE_PUBLISH_INTERVAL
SCHEDULER_ENABLED
```

## HTTP API summary

| Method | Path                          | Purpose                                  |
|--------|-------------------------------|------------------------------------------|
| GET    | /api/config                   | Configuration + broker connection status |
| POST   | /api/publish                  | Enqueue a new message                    |
| GET    | /api/messages/current         | Currently-published message              |
| GET    | /api/messages/display-state   | Latest MQTT-reported display payload     |
| DELETE | /api/messages/{id}            | Remove a queued message                  |
| GET    | /api/scheduler/status         | Scheduler state summary                  |
| GET    | /api/scheduler/stream         | Server-Sent Events stream                |
| GET    | /static/*                     | Static frontend assets                   |
| GET    | /                             | `frontend/static/index.html`             |

See [`BACKEND_SPEC.md §7`](../BACKEND_SPEC.md#7-http-api) for the precise
request/response schemas.

## Behavioural invariants

The implementation respects every invariant in
[`BACKEND_SPEC.md §13`](../BACKEND_SPEC.md#13-key-behavioural-invariants),
including:

* Publish failure does not increment `display_count`.
* The queue snapshot is emitted **before** the dwell sleep.
* The full `display_duration` is always slept — no early wake.
* High-priority messages always dominate the selection.
* SSE clients receive seed events on connect.
* Raw UTF-8 strings are published to MQTT (no JSON wrapping).