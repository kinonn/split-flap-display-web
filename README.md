# Home Split Flap Display - Web Interface

A clean, Google-inspired web interface for controlling a split-flap display via MQTT.

## Features

- **Minimal UI**: Google-style homepage with centered title and input
- **Real-time updates**: Server-Sent Events (SSE) for live display state
- **Character validation**: Only valid split-flap characters accepted
- **Visual feedback**: Typed characters highlight in the valid character set
- **MQTT integration**: Publish commands and subscribe to display state

## Valid Characters

```
 ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789':?!.-/$@#%
```

## Architecture

```
┌─────────────────────────────────────────┐
│  Browser (HTML/JS)                      │
│  - Queue a message (with priority)      │
│  - Admin: view/remove queued messages   │
│  - SSE for live current-message updates │
└─────────────────┬───────────────────────┘
                  │ HTTP / SSE
┌─────────────────▼───────────────────────┐
│  FastAPI Backend                        │
│  - Scheduler (queue + rotation)        │
│  - MQTT client (asyncio-mqtt)           │
│  - Publish to broker per message        │
└─────────────────┬───────────────────────┘
                  │ MQTT (raw string payload)
┌─────────────────▼───────────────────────┐
│  External MQTT Broker                   │
└─────────────────────────────────────────┘
```

The scheduler decides which message to send to the display, in what order,
and for how long. See [Scheduler](#scheduler) below.

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

Edit `backend/app.conf` for your environment:

```env
MQTT_BROKER_HOST=your-broker.example.com
MQTT_BROKER_PORT=1883

MQTT_CLIENT_ID=splitflap-web
PUBLISH_TOPIC=splitflap/splitflap/set
SUBSCRIBE_TOPIC=splitflap/splitflap/state
```

> **Important:** When using `docker compose`, set `MQTT_BROKER_HOST` to a hostname or IP address that is reachable from inside the app container. `localhost` points to the container itself.

### Step 2: Build the Container

```bash
# Build the production image
docker compose build

# Or build with a specific tag
docker compose build --build-arg BUILDKIT_INLINE_CACHE=1
docker tag split-flap-display-web-app:latest split-flap-web:1.0.0
```

The Dockerfile uses a multi-stage build:
1. **Builder stage**: Uses `uv` to resolve and install dependencies into a virtual environment
2. **Runtime stage**: Minimal `python:3.12-slim` image with only the venv and application code

Production hardening included:
- Non-root user (`appuser`) for security
- Health check endpoint (`/api/config`)
- Single worker (required for SSE + in-memory state)

### Step 3: Deploy

```bash
# Start all services in detached mode
docker compose up -d

# Verify services are running
docker compose ps

# Expected output:
# NAME                    STATUS
# split-flap-web          Up (healthy)

# View logs
docker compose logs -f

# Stop services
docker compose down

# Stop and remove volumes
docker compose down -v
```

### Step 4: Verify Deployment

```bash
# Check application health
curl http://localhost:8100/api/config

# Test publishing a message
curl -X POST http://localhost:8100/api/publish \
  -H "Content-Type: application/json" \
  -d '{"payload": "HELLO WORLD"}'
```

### Step 5: Monitoring

**Health Checks:**
- The app container checks `/api/config` every 30 seconds
- The app container auto-restarts on failure (`restart: unless-stopped`)

**Logs:**
```bash
# Application logs
docker compose logs -f app

# All logs
docker compose logs -f
```

**Resource Usage:**
```bash
docker stats split-flap-web
```

### Using an External MQTT Broker

The app requires an external MQTT broker. Configure the broker address in `backend/app.conf`:

```env
MQTT_BROKER_HOST=your-broker.example.com
MQTT_BROKER_PORT=1883
```

> **Note:** `localhost` or `127.0.0.1` refers to the container itself. Use the broker's IP address or a hostname reachable from inside the Docker network. mDNS hostnames (e.g., `rpi41.local`) may not resolve inside containers — use the IP address instead.

### Architecture Notes

**Single Worker Constraint:**
The application must run with exactly **one uvicorn worker** because:
- SSE connections are tracked in-memory per worker
- Message history is stored in a per-process deque
- Multiple workers would each have independent state, breaking real-time updates

**In-Memory State:**
- Message history (up to 500 messages) is not persisted across restarts
- SSE subscribers are tracked per-process
- For high-availability, consider externalizing state to Redis

### Resource Limits

Default limits in `docker-compose.yml`:

| Resource | Limit | Reservation |
|----------|-------|-------------|
| Memory   | 256 MB | 128 MB |
| CPU      | 0.5 cores | 0.25 cores |

Adjust based on your load. The app is lightweight; limits can be increased for environments with many concurrent SSE connections.

### Security Considerations

- The container runs as non-root user `appuser`
- Expose only port 8100 (web) externally; keep port 1883 (MQTT) internal unless needed
- Consider adding a reverse proxy (nginx, Caddy) for HTTPS termination

## Configuration

Edit `backend/app.conf`:

```env
MQTT_BROKER_HOST=localhost
MQTT_BROKER_PORT=1883
MQTT_CLIENT_ID=splitflap-web
PUBLISH_TOPIC=splitflap/splitflap/set
SUBSCRIBE_TOPIC=splitflap/splitflap/state

DEFAULT_DISPLAY_DURATION=10
DEFAULT_TARGET_DISPLAY_COUNT=3
IDLE_MODE=publish
IDLE_MESSAGE=WELCOME
IDLE_PUBLISH_INTERVAL=10
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

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Web interface |
| `POST` | `/api/publish` | Enqueue a message (optional priority) |
| `GET` | `/api/messages` | List active (non-completed) messages |
| `GET` | `/api/messages/all` | List all messages including completed |
| `GET` | `/api/messages/current` | Currently displayed message |
| `POST` | `/api/messages` | Enqueue with custom target/duration/priority |
| `DELETE` | `/api/messages/{id}` | Remove a message from the queue |
| `GET` | `/api/scheduler/status` | Scheduler state, current, queue size |
| `GET` | `/api/scheduler/stream` | SSE stream of scheduler events |
| `GET` | `/api/history` | Recent submissions (who sent what) |
| `GET` | `/api/config` | Current configuration and status |

### POST /api/publish

```json
{
  "payload": "HELLO WORLD",
  "topic": "splitflap/splitflap/set",
  "qos": 0,
  "priority": "normal"
}
```

`priority` is optional and defaults to `"normal"`. Set to `"high"` to jump the queue.

### POST /api/messages

```json
{
  "text": "HAPPY BIRTHDAY",
  "targetDisplayCount": 5,
  "displayDuration": 15,
  "priority": "high"
}
```

All fields except `text` are optional and fall back to `DEFAULT_*` values from config.

## Scheduler

The scheduler is the only component that decides what appears on the display.

### Message lifecycle

```
Pending  →  Active  →  Completed
```

* **Pending**: new message, not yet displayed
* **Active**: has been displayed at least once
* **Completed**: reached its `targetDisplayCount`; never scheduled again

### Selection rule

The scheduler chooses the next message to display using this priority order:

1. **Priority** — `"high"` messages are preferred over `"normal"`.
2. **displayCount** — among same priority, the message with the lowest count wins (fairness).
3. **createdAt** — among same priority and count, the oldest message wins (no starvation).

### Priority behavior

* **High-priority messages are picked up as soon as possible.**
* If the scheduler is **idle** when a high-priority message arrives, the idle sleep is
  interrupted via an internal event; the message is published within milliseconds.
* If a high-priority message arrives while another message is **currently being displayed**,
  the in-flight message finishes its full `displayDuration` (per spec: "the currently
  displayed message is never interrupted"). The high-priority message is selected on the
  very next tick.

### Idle behavior

* `IDLE_MODE=publish` (default): the configured `IDLE_MESSAGE` is published every
  `IDLE_PUBLISH_INTERVAL` seconds while the queue is empty.
* `IDLE_MODE=keep`: nothing is published; the display keeps showing the last message.

### Persistence

This MVP is **in-memory only**. The queue is lost on restart. Adding persistent
storage (SQLite or JSON) is a future extension.

## Tests

```bash
# from the repo root
python -m unittest discover -s backend/tests -t .
```

Tests use the standard library `unittest` framework. No external dependencies required.

Coverage:

- `test_message_and_store.py` — `Message.to_dict()`, `MessageStore` CRUD
- `test_add_remove.py` — `add_message` validation, defaults, priority; `remove_message` behavior
- `test_selection.py` — selection algorithm: priority dominates, count, age tiebreak, completed exclusion
- `test_tick.py` — `scheduler_tick` lifecycle, idle handler, run loop, wake-up, publish failure
- `test_state.py` — `state()`, `high_priority_count()`, accessors
- `test_subscribers.py` — SSE subscriber notifications

## Project Structure

```
split-flap-display-web/
├── backend/
│   ├── app/
│   │   ├── config.py          # Settings from app.conf
│   │   ├── models.py          # Pydantic schemas (incl. priority)
│   │   ├── mqtt_client.py     # Async MQTT wrapper
│   │   ├── scheduler.py       # Queue, rotation, priority logic
│   │   ├── scheduler_api.py   # Admin /api/messages and /api/scheduler/* routes
│   │   └── main.py            # FastAPI application
│   ├── tests/                 # unittest-based scheduler tests
│   ├── requirements.txt       # pip dependencies (alternative to uv)
│   └── app.conf.example       # Configuration template
├── frontend/
│   └── static/
│       ├── index.html         # Main page
│       ├── app.js             # Frontend logic
│       └── style.css          # Styling
├── Dockerfile                 # Multi-stage production build with uv
├── docker-compose.yml         # Production: App service
├── pyproject.toml             # uv project config
└── uv.lock                    # Locked dependency versions
```

## License

MIT
