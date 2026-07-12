# Home Split Flap Display - Web Interface

A clean, Google-inspired web interface for controlling a split-flap display via MQTT.
The single page lets anyone queue a message, watch the live display state, and
manage the queue (view, remove, and inspect message history) in one place.

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
|  FastAPI Backend                        |
|  - Scheduler (queue + rotation)         |
|  - MQTT client (asyncio-mqtt)           |
|  - Publish to broker per message        |
+--------------------+--------------------+
                     | MQTT (raw string payload)
+--------------------v--------------------+
|  External MQTT Broker                   |
+-----------------------------------------+
```

The scheduler is the only component that decides what appears on the display,
in what order, and for how long. See [Scheduler](#scheduler) below.

## Quick Start

### Using uv (Recommended)

```bash
# Install uv if not already installed
curl -LsSf https://astral.sh/uv/install.sh | sh

# Clone and setup
git clone <repo-url>
cd split-flap-display-web

# Create virtual environment and install dependencies
uv sync

# Configure environment
cp backend/app.conf.example backend/app.conf
# Edit backend/app.conf with your MQTT broker settings

# Run the server
uv run uvicorn backend.app.main:app --reload --port 8000
```

### Using pip

```bash
# Clone and setup
git clone <repo-url>
cd split-flap-display-web

# Create virtual environment
python -m venv .venv
source .venv/bin/activate  # Linux/Mac
# .venv\Scripts\activate   # Windows

# Install dependencies
pip install -r backend/requirements.txt

# Configure environment
cp backend/app.conf.example backend/app.conf
# Edit backend/app.conf with your MQTT broker settings

# Run the server
uvicorn backend.app.main:app --reload --port 8000
```

### Using Docker (Production)

```bash
# Configure environment
cp backend/app.conf.example backend/app.conf
# Edit backend/app.conf with your MQTT broker settings

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
cp backend/app.conf.example backend/app.conf
```

Edit `backend/app.conf` for your environment. At a minimum, set
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

The Dockerfile uses a multi-stage build:

1. **Builder stage**: Uses `uv` to resolve and install dependencies into a
   virtual environment.
2. **Runtime stage**: Minimal `python:3.12-slim` image with only the venv and
   application code.

Production hardening included:

- Non-root user (`appuser`) for security
- Health check endpoint (`/api/config`)
- Single worker (required for SSE + in-memory state)

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
`backend/app.conf`:

```env
MQTT_BROKER_HOST=your-broker.example.com
MQTT_BROKER_PORT=1883
```

> **Note:** `localhost` or `127.0.0.1` refers to the container itself. Use the
> broker's IP address or a hostname reachable from inside the Docker network.
> mDNS hostnames (e.g., `rpi.local`) may not resolve inside containers - use
> the IP address instead.

### Architecture Notes

**Single Worker Constraint:**

The application must run with exactly **one uvicorn worker** because:

- SSE connections are tracked in-memory per worker.
- Message history is stored in a per-process deque.
- Multiple workers would each have independent state, breaking real-time
  updates.

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

Adjust based on your load. The app is lightweight; limits can be increased
for environments with many concurrent SSE connections.

### Security Considerations

- The container runs as non-root user `appuser`.
- Expose only port 8100 (web) externally; keep port 1883 (MQTT) internal unless
  needed.
- Consider adding a reverse proxy (nginx, Caddy) for HTTPS termination.
- The web UI has no built-in authentication. If you expose it beyond a trusted
  network, put it behind a reverse proxy that enforces auth (the backend
  honors a `Cf-Access-Authenticated-User-Email` header for per-message user
  attribution when present).

## Configuration

The full `backend/app.conf` is shown below. The shipped `backend/app.conf.example`
is the source of truth for the recommended values; copy it to `backend/app.conf`
and edit as needed. Any variable can also be supplied via the environment.

```env
MQTT_BROKER_HOST=localhost
MQTT_BROKER_PORT=1883
MQTT_CLIENT_ID=splitflap-web
PUBLISH_TOPIC=splitflap/splitflap/set
SUBSCRIBE_TOPIC=splitflap/splitflap/state

# Scheduler defaults
DEFAULT_DISPLAY_DURATION=10
DEFAULT_TARGET_DISPLAY_COUNT=3

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
| `DEFAULT_TARGET_DISPLAY_COUNT` | How many times each new message is shown | `3` |
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
| `GET` | `/api/messages/current` | Currently displayed message, if any |
| `DELETE` | `/api/messages/{id}` | Remove a message from the queue |
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

### GET /api/scheduler/stream

SSE event names:

| Event | Payload | Source |
|-------|---------|--------|
| `current` | `{ "message": <Message> \| null }` | Scheduler: what the scheduler is currently publishing |
| `queue` | `{ "messages": [<Message>, ...] }` | Scheduler: all active (non-completed) messages |
| `history` | `{ "messages": [<Message>, ...] }` | Scheduler: recent submissions, most recent first |
| `display-state` | `{ "message": { "message": "<payload>" } }` | MQTT feedback: what the display is actually showing |

`<Message>` is the same shape produced by `Message.to_dict()`: `id`, `message`,
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

```bash
# from the repo root
python -m unittest discover -s backend/tests -t .
```

Tests use the standard library `unittest` framework. No external dependencies
required.

Coverage:

- `test_message_and_store.py` - `Message.to_dict()`, `MessageStore` CRUD
- `test_add_remove.py` - `add_message` validation, defaults, priority;
  `remove_message` behavior
- `test_selection.py` - selection algorithm: priority dominates, count, age
  tiebreak, completed exclusion
- `test_tick.py` - `scheduler_tick` lifecycle, idle handler, run loop,
  wake-up, publish failure
- `test_state.py` - `state()`, `high_priority_count()`, accessors
- `test_subscribers.py` - SSE subscriber notifications
- `test_mqtt_client.py` - MQTT display-state subscription and event dispatch
- `test_sse_merge.py` - SSE event-stream merge logic (scheduler + MQTT queues)

## Project Structure

```
split-flap-display-web/
+-- backend/
|   +-- app/
|   |   +-- config.py          # Settings loaded from app.conf / environment
|   |   +-- models.py          # Pydantic request/response schemas
|   |   +-- mqtt_client.py     # Async MQTT wrapper
|   |   +-- scheduler.py       # Queue, rotation, priority logic
|   |   +-- scheduler_api.py   # /api/messages, /api/scheduler/* routes
|   |   +-- main.py            # FastAPI application entrypoint
|   +-- tests/                 # unittest-based scheduler and client tests
|   +-- requirements.txt       # pip dependencies (alternative to uv)
|   +-- app.conf.example       # Configuration template
+-- frontend/
|   +-- static/
|       +-- index.html         # Single-page UI
|       +-- app.js             # Frontend logic
|       +-- style.css          # Styling
+-- Dockerfile                 # Multi-stage production build with uv
+-- docker-compose.yml         # Production: App service
+-- pyproject.toml             # uv project config
+-- uv.lock                    # Locked dependency versions
```

## License

MIT