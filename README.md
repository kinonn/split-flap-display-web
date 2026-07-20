# Home Split Flap Display - Web Interface

A simple web interface for sending messages to a split-flap display via MQTT.
The single page lets anyone queue a message, watch the live display state, and
manage the queue (view, remove, and inspect message history) in one place.

The backend is implemented in **Go** using the [Fiber](https://gofiber.io)
web framework. The source lives under [`backend-go/`](backend-go/); a legacy
Python implementation remains under [`backend/`](backend/) for reference but is
no longer used by `docker compose` or the production image.

## Features

- **Single-page UI**: Title, live "Now showing" panel, character picker, queue
  composer, and queue/history viewer all live on one page (`/`).
- **Real-time updates**: Server-Sent Events (SSE) push queue, current, and history
  changes; MQTT feedback from the display drives the "Now showing" text.
- **Character validation**: Only valid split-flap characters are accepted; typed
  characters briefly highlight in the valid character set.
- **Queue management**: View, remove, and inspect the live queue and a rolling
  message history (last 50 submissions) without leaving the page.
- **Priority**: Toggle "High priority" to jump the queue.
- **MQTT integration**: Publishes commands to the display and subscribes to its
  state topic.
- **Small static binary**: the Go backend ships as a single ~15 MB binary with
  no runtime interpreter, so the production Docker image is ~20 MB.

## Valid Characters

```
 ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789':?!.-/$@#%
```

## Architecture

```
+-----------------------------------------+
|  Browser (single page: /)               |
|  - Compose a message (with priority)    |
|  - View live display state (SSE + MQTT) |
|  - View & manage the queue              |
|  - View message history                 |
+--------------------+--------------------+
                     | HTTP / SSE
+--------------------v--------------------+
|  Go Backend (Fiber)                    |
|  - Scheduler (queue + rotation)        |
|  - paho.mqtt client                    |
|  - Publish to broker per message       |
+--------------------+--------------------+
                     | MQTT (raw string payload)
+--------------------v--------------------+
|  External MQTT Broker                   |
+-----------------------------------------+
```

The scheduler is the only component that decides what appears on the display,
in what order, and for how long. See [Scheduler](#scheduler) below.

## Quick Start

### From source (Go)

Requirements: Go 1.22 or newer.

```bash
# Clone
git clone <repo-url>
cd split-flap-display-web

# Configure
cp backend-go/app.conf.example backend-go/app.conf
# Edit backend-go/app.conf with your MQTT broker settings

# Run (from backend-go/ so app.conf is picked up)
cd backend-go
go run ./cmd/server
```

Open http://localhost:8100 in your browser.

### Build a binary

```bash
cd backend-go
go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server
./bin/server
```

Cross-compile a static Linux binary for Docker / Raspberry Pi:

```bash
cd backend-go
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server
```

### Using Docker (Production)

```bash
# Configure environment (optional — env vars override file values)
cp backend-go/app.conf.example backend-go/app.conf
# Edit backend-go/app.conf with your MQTT broker settings

# Build and run the app
docker compose up -d --build

# Check status
docker compose ps
docker compose logs -f app
```

Open http://localhost:8100 in your browser.

## Production Deployment

### Prerequisites

- Docker Engine 20.10+ and Docker Compose v2+
- An external MQTT broker reachable from the app container

### Step 1: Prepare Configuration

```bash
# Clone the repository
git clone <repo-url>
cd split-flap-display-web

# Create production configuration file
cp backend-go/app.conf.example backend-go/app.conf
```

Edit `backend-go/app.conf` for your environment. At a minimum, set
`MQTT_BROKER_HOST` to a hostname or IP address that is reachable from inside
the app container (see the warning below). See
[Configuration](#configuration) for the full file and the list of variables.

> **Important:** When using `docker compose`, `localhost` inside `app.conf`
> points to the app container itself, not your host. Use the broker's IP or a
> hostname reachable from inside the Docker network.

### Step 2: Build the Container

```bash
# Build the production image
docker compose build
```

The Dockerfile (`backend-go/Dockerfile`) uses a multi-stage build:

1. **Builder stage**: `golang:1.22-alpine` compiles a static binary with
   `CGO_ENABLED=0` and trimmed symbols.
2. **Runtime stage**: Minimal `alpine:3.20` image with only the binary,
   `app.conf`, and the bundled static frontend.

Production hardening included:

- Non-root user (`app`) for security.
- `wget`-based health check endpoint (`/api/config`) every 30 s.
- Static binary — no interpreter, no separate runtime dependencies.
- Single process — required for SSE + in-memory state.

### Step 3: Deploy

```bash
# Start the service in detached mode
docker compose up -d

# Verify the service is running
docker compose ps

# View logs
docker compose logs -f

# Stop
docker compose down
```

### Step 4: Verify Deployment

```bash
# Check application health
curl http://localhost:8100/api/config

# Queue a message
curl -X POST http://localhost:8100/api/publish \
  -H "Content-Type: application/json" \
  -d '{"text": "HELLO WORLD", "priority": "normal"}'
```

### Step 5: Monitoring

**Health Checks:**

- The app container checks `/api/config` every 30 seconds.
- The app container auto-restarts on failure (`restart: unless-stopped`).

**Logs:**

```bash
# Application logs
docker compose logs -f app
```

**Resource Usage:**

```bash
docker stats split-flap-web
```

### Using an External MQTT Broker

The app requires an external MQTT broker. Configure the broker address in
`backend-go/app.conf`:

```env
MQTT_BROKER_HOST=your-broker.example.com
MQTT_BROKER_PORT=1883
```

> **Note:** `localhost` or `127.0.0.1` refers to the container itself. Use the
> broker's IP address or a hostname reachable from inside the Docker network.
> mDNS hostnames (e.g., `rpi.local`) may not resolve inside containers - use
> the IP address instead.

### Architecture Notes

**Single-Process Constraint:**

The application must run as a **single process** because:

- SSE connections are tracked in-memory per process.
- Message history is stored in a per-process ring buffer.
- Multiple processes would each have independent state, breaking real-time
  updates.

The Fiber server uses goroutines for concurrency and never needs
horizontal scaling for the typical home deployment.

**In-Memory State:**

- Message history (up to 50 entries) is not persisted across restarts.
- SSE subscribers are tracked per-process.
- For high availability, consider externalizing state to Redis.

### Resource Limits

Default limits in `docker-compose.yml`:

| Resource | Limit      | Reservation |
|----------|------------|-------------|
| Memory   | 256 MB     | 128 MB      |
| CPU      | 0.5 cores  | 0.25 cores  |

The Go backend typically idles well below 32 MB of RAM; adjust based on how
many concurrent SSE connections you expect.

### Security Considerations

- The container runs as non-root user `app`.
- Expose only port 8100 (web) externally; keep port 1883 (MQTT) internal unless
  needed.
- Consider adding a reverse proxy (nginx, Caddy) for HTTPS termination.
- The web UI has no built-in authentication. If you expose it beyond a trusted
  network, put it behind a reverse proxy that enforces auth (the backend
  honors a `Cf-Access-Authenticated-User-Email` header for per-message user
  attribution when present).

## Configuration

The full `backend-go/app.conf` is shown below. The shipped
`backend-go/app.conf.example` is the source of truth for the recommended values;
copy it to `backend-go/app.conf` and edit as needed. Any variable can also be
supplied via the environment (highest precedence).

```env
MQTT_BROKER_HOST=localhost
MQTT_BROKER_PORT=1883
MQTT_CLIENT_ID=splitflap-web
PUBLISH_TOPIC=splitflap/splitflap/set
SUBSCRIBE_TOPIC=splitflap/splitflap/state

# Scheduler defaults
DEFAULT_DISPLAY_DURATION=10
DEFAULT_TARGET_DISPLAY_COUNT=6

# Idle behavior: "publish" publishes IDLE_MESSAGE repeatedly; "keep" leaves the display alone
IDLE_MODE=publish
IDLE_MESSAGE=WELCOME
IDLE_PUBLISH_INTERVAL=10

# Set to false to disable the scheduler loop (e.g. for raw /api/publish only)
SCHEDULER_ENABLED=true
```

| Variable | Description | Default |
|----------|-------------|---------|
| `MQTT_BROKER_HOST` | MQTT broker hostname | `localhost` |
| `MQTT_BROKER_PORT` | MQTT broker port | `1883` |
| `MQTT_CLIENT_ID` | Client ID for MQTT connection | `splitflap-web` |
| `PUBLISH_TOPIC` | Topic to send display commands | `splitflap/splitflap/set` |
| `SUBSCRIBE_TOPIC` | Topic to receive display state | `splitflap/splitflap/state` |
| `DEFAULT_DISPLAY_DURATION` | Seconds each message stays up | `10` |
| `DEFAULT_TARGET_DISPLAY_COUNT` | How many times each new message is shown | `6` |
| `IDLE_MODE` | `publish` (idle message) or `keep` (last shown) | `publish` |
| `IDLE_MESSAGE` | Message shown in idle state | `WELCOME` |
| `IDLE_PUBLISH_INTERVAL` | Seconds between idle republishes | `10` |
| `SCHEDULER_ENABLED` | Run the scheduler loop | `true` |

## API Endpoints

The web UI consumes all of these endpoints. There is no separate admin page;
the queue, current, and history are all managed from `/`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Single-page web interface |
| `GET` | `/static/*` | Frontend assets (JS, CSS) |
| `GET` | `/api/config` | Current configuration and connection status |
| `POST` | `/api/publish` | Enqueue a message (with optional priority) |
| `GET` | `/api/messages/current` | Scheduler's current message (queue state), if any — **not necessarily what's on the display** |
| `GET` | `/api/messages/display-state` | Actual message currently on the physical display (from firmware MQTT state) |
| `DELETE` | `/api/messages/{id}` | Remove a **queued** message (by scheduler ID) |
| `GET` | `/api/scheduler/status` | Scheduler state, current, queue size, high-priority count |
| `GET` | `/api/scheduler/stream` | SSE stream of `current`, `queue`, `history`, and `display-state` events |

Queue and history listings are not exposed as REST endpoints - the server
pushes them to clients over the SSE stream on every change.

### POST /api/publish

```json
{
  "text": "HELLO WORLD",
  "priority": "normal"
}
```

All fields except `text` are optional:

| Field | Type | Description |
|-------|------|-------------|
| `text` | string | The message to display. May also be sent as `payload` for compatibility. |
| `payload` | string | Alias for `text`; used if `text` is not provided. |
| `target_display_count` | int | How many times to show the message. Defaults to `DEFAULT_TARGET_DISPLAY_COUNT`. |
| `display_duration` | int | Seconds each show lasts. Defaults to `DEFAULT_DISPLAY_DURATION`. |
| `priority` | `"normal"` \| `"high"` | `"high"` jumps the queue. Defaults to `"normal"`. |

Returns `{ "status": "ok", "id": "<uuid>" }` on success.

### GET /api/messages/display-state

Returns the latest message from the display firmware's MQTT retained state topic
(`splitflap/splitflap/state`). This reflects what the **physical display is
actually showing**, which persists until the next write — unlike
`/api/messages/current` which clears when the scheduler's message completes its
display cycle.

```json
{ "message": "HELLO WORLD" }
```

If no message has been received from the firmware yet, returns `null`.

### GET /api/scheduler/stream

SSE event names:

| Event | Payload | Source |
|-------|---------|--------|
| `current` | `{ "message": <Message> \| null }` | Scheduler: the message the scheduler has *just published* (queue state — clears when the message completes its `target_display_count`) |
| `queue` | `{ "messages": [<Message>, ...] }` | Scheduler: all active (non-completed) messages |
| `history` | `{ "messages": [<Message>, ...] }` | Scheduler: recent submissions, most recent first |
| `display-state` | `{ "message": { "message": "<payload>" } }` | MQTT feedback: what the display is **actually showing** (from firmware's retained `splitflap/splitflap/state` topic — persists until next write) |

`<Message>` is the same shape produced by `Message.ToDTO()`: `id`, `message`,
`createdAt`, `status`, `displayDuration`, `targetDisplayCount`, `displayCount`,
`lastDisplayedAt`, `lastDisplayedTime`, `priority`, `user`.

## Scheduler

The scheduler is the only component that decides what appears on the display.

### Message lifecycle

```
Pending  ->  Active  ->  Completed
```

- **Pending**: new message, not yet displayed.
- **Active**: has been displayed at least once.
- **Completed**: reached its `target_display_count`; never scheduled again.

### Selection rule

The scheduler chooses the next message to display using this priority order:

1. **Priority** - `"high"` messages are preferred over `"normal"`.
2. **displayCount** - among same priority, the message with the lowest count
   wins (fairness).
3. **createdAt** - among same priority and count, the oldest message wins (no
   starvation).

### Priority behavior

- **High-priority messages are picked up as soon as possible.**
- If the scheduler is **idle** when a high-priority message arrives, the idle
  sleep is interrupted via an internal event; the message is published within
  milliseconds.
- If a high-priority message arrives while another message is **currently
  being displayed**, the in-flight message finishes its full
  `display_duration` (per spec: "the currently displayed message is never
  interrupted"). The high-priority message is selected on the very next tick.

### Idle behavior

- `IDLE_MODE=publish` (default): the configured `IDLE_MESSAGE` is published
  every `IDLE_PUBLISH_INTERVAL` seconds while the queue is empty.
- `IDLE_MODE=keep`: nothing is published; the display keeps showing the last
  message.

### Persistence

This MVP is **in-memory only**. The queue and history are lost on restart.
Adding persistent storage (SQLite or JSON) is a future extension.

## Tests

The Go backend ships with unit tests under `backend-go/internal/`. From the
`backend-go/` directory:

```bash
go vet ./...
go test ./... -timeout 30s
```

Coverage:

- `internal/config/config_test.go` — config file parsing, env overrides,
  defaults.
- `internal/scheduler/scheduler_test.go` — message validation, defaults,
  history, selection algorithm (priority/count/age), queue snapshot ordering,
  state, remove, wakeup, Start/Stop lifecycle.

The tests stub the MQTT client and never require a live broker.

## Project Structure

```
split-flap-display-web/
+-- backend-go/                 # Go (Fiber) backend - primary implementation
|   +-- cmd/server/main.go      # entry point: lifecycle + signal handling
|   +-- internal/
|   |   +-- config/             # app.conf + env loader with defaults
|   |   +-- models/             # Message, Priority, HistoryEntry, DTOs
|   |   +-- store/              # thread-safe in-memory message store
|   |   +-- queue/              # bounded drop-oldest pub/sub queue
|   |   +-- mqttclient/         # paho wrapper: connect/publish/subscribe
|   |   +-- scheduler/          # tick loop, idle handling, SSE subs
|   |   +-- api/                # Fiber routes + SSE handler
|   +-- app.conf                # configuration file (defaults)
|   +-- app.conf.example        # annotated configuration template
|   +-- Dockerfile              # multi-stage production build
|   +-- go.mod / go.sum         # module pins
+-- backend/                    # legacy Python (FastAPI) implementation
+-- frontend/
|   +-- static/
|       +-- index.html          # Single-page UI
|       +-- app.js              # Frontend logic
|       +-- style.css           # Styling
+-- docker-compose.yml          # Production: builds backend-go/Dockerfile
+-- BACKEND_SPEC.md             # Backend requirements specification
+-- pyproject.toml / uv.lock    # legacy Python project pins
```

See [`backend-go/README.md`](backend-go/README.md) for backend-specific build,
test and deployment instructions.

## License

MIT