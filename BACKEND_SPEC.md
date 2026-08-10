# Split-Flap Display Web Backend — Requirements Specification

## Purpose

This specification fully describes the backend of the **split-flap-display-web** project. A Go implementation must satisfy every requirement herein without referencing the original Python source.

---

## 1. System Overview

The backend is an HTTP server that:

1. Accepts text messages via a REST API.
2. Schedules those messages for display on a physical split-flap display.
3. Publishes messages to an MQTT broker so the display hardware can render them.
4. Subscribes to an MQTT state topic to receive feedback from the display.
5. Pushes real-time state updates to connected web clients via Server-Sent Events (SSE).
6. Serves a static frontend (HTML/JS/CSS) from a `frontend/static/` directory.

The server listens on **port 8100**.

---

## 2. Configuration

### 2.1 Configuration File

The server reads configuration from a file named `app.conf` located in the same directory as the binary (or working directory). The file uses a simple `KEY=VALUE` format (one setting per line). Lines beginning with `#` are comments.

### 2.2 Configuration Settings

| Key | Type | Default | Description |
|---|---|---|---|
| `MQTT_BROKER_HOST` | string | `"localhost"` | Hostname of the MQTT broker |
| `MQTT_BROKER_PORT` | int | `1883` | Port of the MQTT broker |
| `MQTT_CLIENT_ID` | string | `"splitflap-web"` | MQTT client identifier |
| `PUBLISH_TOPIC` | string | `"splitflap/splitflap/set"` | MQTT topic to publish messages to the display |
| `SUBSCRIBE_TOPIC` | string | `"splitflap/splitflap/state"` | MQTT topic to subscribe for display state feedback |
| `DEFAULT_DISPLAY_DURATION` | int | `10` | Default seconds each message stays on the display per cycle |
| `DEFAULT_TARGET_DISPLAY_COUNT` | int | `6` | Default number of times each message should be displayed before completion |
| `IDLE_MESSAGE` | string | `"WELCOME"` | Message to publish when the scheduler is idle (in "publish" mode) |
| `IDLE_MODE` | string | `"publish"` | Idle behavior: `"publish"` repeatedly sends IDLE_MESSAGE; `"keep"` does nothing |
| `IDLE_PUBLISH_INTERVAL` | int | `10` | Seconds between idle message re-publishes |
| `SCHEDULER_ENABLED` | bool | `true` | Whether the scheduler loop runs at startup |

### 2.3 Configuration Precedence

1. If `app.conf` exists, read it.
2. Environment variables override file values (standard env-var override behavior).
3. If neither file nor env var is set, use the default from the table above.

### 2.4 Startup Logging

On startup, log the resolved values of all configuration settings at INFO level.

---

## 3. Data Models

### 3.1 Message

A `Message` represents a text to be shown on the split-flap display.

| Field | Type | Description |
|---|---|---|
| `id` | UUID | Unique identifier, generated on creation |
| `message` | string | The text content to display |
| `created_at` | datetime (UTC or local) | Timestamp of creation |
| `status` | MessageStatus | Current lifecycle status |
| `display_duration` | int | Seconds to keep this message on the display per cycle |
| `target_display_count` | int | Number of display cycles before the message is completed |
| `display_count` | int | Number of times the message has been displayed so far (starts at 0) |
| `last_displayed_at` | datetime or null | Timestamp of the most recent display cycle |
| `priority` | Priority | `"normal"` or `"high"` |
| `user` | string | Username who submitted the message (extracted from auth header or `"unknown"`) |

### 3.2 MessageStatus (Enum)

| Value | Meaning |
|---|---|
| `"Pending"` | Created but never displayed |
| `"Active"` | Has been displayed at least once but has not yet reached target_display_count |
| `"Completed"` | Has been displayed target_display_count times, or was manually removed |

### 3.3 Priority

Two values: `"normal"` and `"high"`.

Priority ranking for selection purposes:
- `"normal"` → rank 0
- `"high"` → rank 1

Higher rank wins over lower rank regardless of other factors.

### 3.4 Message Serialization (to_dict / JSON)

When serialized to JSON, a Message produces:

```json
{
  "id": "uuid-string",
  "message": "HELLO",
  "createdAt": "2026-01-02T03:04:05",
  "status": "Pending",
  "displayDuration": 10,
  "targetDisplayCount": 3,
  "displayCount": 0,
  "lastDisplayedAt": "2026-01-02T03:04:05" | null,
  "lastDisplayedTime": "03:04" | null,
  "priority": "normal",
  "user": "unknown"
}
```

- `createdAt`: ISO 8601 format
- `lastDisplayedAt`: ISO 8601 format or null
- `lastDisplayedTime`: `"HH:MM"` format or null (derived from `last_displayed_at`)

---

## 4. MQTT Client

### 4.1 Connection Lifecycle

- On server start, initiate an async background task that connects to the MQTT broker.
- Use the configured `MQTT_BROKER_HOST`, `MQTT_BROKER_PORT`, and `MQTT_CLIENT_ID`.
- Set MQTT keepalive to 30 seconds.
- On successful connection, subscribe to the configured `SUBSCRIBE_TOPIC`.
- If the connection fails, log a warning (first attempt) or debug (subsequent attempts), wait 5 seconds, and retry indefinitely.
- On cancellation (server shutdown), disconnect cleanly.

### 4.2 Publishing

- Expose a `publish(topic, payload, qos)` method.
- The payload is a **raw UTF-8 string** (NOT JSON-encoded).
- If not connected, return an error (the caller handles retry logic).

### 4.3 Subscribing — Display State

- When a message arrives on `SUBSCRIBE_TOPIC`, decode the payload as UTF-8.
- Store the payload as `_latest_message` (the most recent display state).
- Maintain a history ring buffer of the last 500 received messages. Each entry is:
  ```json
  {"topic": "splitflap/splitflap/state", "payload": "HELLO"}
  ```

### 4.4 Display-State Subscriber System

- Allow callers to subscribe to "display-state" events via a queue-based pub/sub mechanism.
- `subscribe_display_state()`:
  - Creates a new queue (max size 100).
  - If `_latest_message` is not null, seed the queue with:
    ```json
    {"type": "display-state", "message": {"message": "<latest_message>"}}
    ```
  - Register the queue for future notifications.
  - Return the queue.
- `unsubscribe_display_state(queue)`:
  - Remove the queue from the subscriber set. Safe to call with unknown queues.
- When a new MQTT message arrives on the subscribe topic, push a `"display-state"` event to all registered queues.
  - If a queue is full, drop the oldest event and retry once.

### 4.5 History Subscriber System (Legacy)

- Allow callers to subscribe to the raw MQTT message history via a separate queue-based pub/sub.
- `subscribe_queue()`:
  - Creates a new queue (max size 500).
  - Seed with all 500 entries from the history ring buffer.
  - Register for future notifications.
  - Return the queue.
- `unsubscribe_queue(queue)`:
  - Remove from subscriber set.
- When a new MQTT message arrives, push `{"topic": "...", "payload": "..."}` to all registered queues.
  - If a queue is full, drop the oldest event and retry once.

### 4.6 Public Accessors

- `connected` → bool: whether currently connected to the broker.
- `get_latest_message()` → string or null: the most recent payload from the subscribe topic.
- `get_history()` → list: copy of the history ring buffer.

---

## 5. Message Store

An in-memory, concurrency-safe store for all messages.

### 5.1 Operations

| Operation | Description |
|---|---|
| `add(message)` | Store a message by its UUID |
| `get(id)` → Message or null | Retrieve a message by UUID |
| `list_active()` → []Message | Return all messages where status != Completed |
| `list_all()` → []Message | Return all messages |
| `mark_completed(id)` → bool | Set status to Completed; return false if ID not found |
| `update_fields(id, **kwargs)` | Update arbitrary fields on a message; no-op if ID not found |

### 5.2 Concurrency

All operations must be protected by a mutex/lock to ensure thread/goroutine safety.

---

## 6. Scheduler

The core scheduling engine that manages message lifecycle and MQTT publishing.

### 6.1 Initialization

The scheduler is constructed with:
- A reference to the MQTT client
- `publish_topic`: the MQTT topic to publish to
- `default_display_duration`: fallback display duration
- `default_target_display_count`: fallback target display count
- `idle_message`: message for idle mode
- `idle_mode`: `"publish"` or `"keep"`
- `idle_publish_interval`: seconds between idle publishes

### 6.2 Internal State

- `_current`: the Message currently being displayed (or null)
- `_store`: the MessageStore
- `_wakeup`: an event/signal used to interrupt idle sleep when a new message arrives
- `_subscribers`: set of queues for SSE event push
- `_history`: ring buffer (max 50 entries) of sent message metadata

### 6.3 History Ring Buffer

Each entry is:
```json
{
  "id": "uuid-string",
  "time": "HH:MM",
  "user": "alice",
  "message": "HELLO",
  "priority": "normal"
}
```
New entries are prepended (most recent first). Maximum 50 entries.

### 6.4 add_message(text, target_display_count?, display_duration?, priority?, user?)

1. Validate:
   - `text` must be non-empty after stripping whitespace → else return error `"text must be non-empty"`
   - `target_display_count` (if provided) must be > 0 → else error `"target_display_count must be > 0"`
   - `display_duration` (if provided) must be > 0 → else error `"display_duration must be > 0"`
   - `priority` must be `"normal"` or `"high"` → else error `"priority must be 'normal' or 'high'"`
2. Apply defaults: use configured defaults for `target_display_count` and `display_duration` if not provided.
3. Create a new Message with:
   - New random UUID
   - `status` = `"Pending"`
   - `display_count` = 0
   - `last_displayed_at` = null
   - `created_at` = now
4. Store the message in the MessageStore.
5. Prepend an entry to the history ring buffer.
6. Signal the `_wakeup` event (to interrupt idle sleep).
7. Notify all SSE subscribers with:
   - `{"type": "queue", "messages": <queue_snapshot>}`
   - `{"type": "history", "messages": <history_snapshot>}`
8. Return the message UUID.

### 6.5 remove_message(id)

1. Mark the message as Completed in the store.
2. If found and marked, notify SSE subscribers with `{"type": "queue", "messages": <queue_snapshot>}`.
3. Return true if found, false otherwise.

### 6.6 Message Selection Algorithm (`select_next_message`)

From all non-Completed messages, select the one with the **highest priority** for display. The selection uses a multi-key sort:

**Sort key** (ascending, pick the minimum):
1. **Priority rank** (descending — negate it): high priority (rank 1) beats normal (rank 0). Use `-PRIORITY_RANK[priority]` so higher priority sorts first.
2. **display_count** (ascending): among same priority, the message displayed fewer times wins.
3. **created_at** (ascending): among full ties, the oldest message wins.

The message with the smallest composite key `(-priority_rank, display_count, created_at)` is selected.

If no non-Completed messages exist, return null.

**Important**: The currently-displayed message is NOT excluded from selection. It can be re-selected, but its incremented `display_count` naturally causes it to sink in the sort order.

### 6.7 Scheduler Tick (`scheduler_tick`)

This is the core loop iteration. Each tick:

1. Call `select_next_message()`.
2. **If no message is selected** → call `_handle_idle()` and return.
3. Set `_current` = selected message.
4. Notify subscribers: `{"type": "current", "message": <message.to_dict()>}`.
5. Publish the message text to MQTT on `publish_topic` with QoS 0.
   - **If publish fails**:
     - Log a warning.
     - Set `_current` = null.
     - Notify subscribers: `{"type": "current", "message": null}`.
     - Sleep 1 second (backoff).
     - Return (do NOT increment display_count, do NOT set last_displayed_at).
6. **On successful publish**:
   - Set `last_displayed_at` = now.
   - Notify subscribers: `{"type": "current", "message": <message.to_dict()>}`.
   - Increment `display_count` by 1.
   - If `display_count` >= 1 and status was `"Pending"`, set status to `"Active"`.
   - Notify subscribers: `{"type": "queue", "messages": <queue_snapshot>}`.
     - **This queue snapshot must be emitted BEFORE the display_duration sleep**, so the UI sees the updated count immediately.
7. Sleep for `display_duration` seconds (the full duration — no early wake).
8. **After the sleep**:
   - If `display_count` >= `target_display_count`, set status to `"Completed"` and set `_current` = null.
   - Notify subscribers: `{"type": "queue", "messages": <queue_snapshot>}` (if completed).
   - Notify subscribers: `{"type": "current", "message": <_current.to_dict() or null>}`.

### 6.8 Idle Handling (`_handle_idle`)

Called when `select_next_message()` returns null (no active messages).

1. If `_current` is not null, set it to null and notify: `{"type": "current", "message": null}`.

2. **If `idle_mode` == `"keep"`**:
   - Wait for `_wakeup` event with a 1-second timeout.
   - If woken, clear the event and return (a new message has arrived).
   - If timeout, just return (the run loop will call tick again).

3. **If `idle_mode` == `"publish"`**:
   - Publish `idle_message` to MQTT on `publish_topic` with QoS 0.
     - If publish fails, silently ignore the error.
   - Wait for `_wakeup` event with a timeout of `idle_publish_interval` seconds.
   - If woken, clear the event and return.
   - If timeout, return (the run loop will re-enter tick, which will re-enter idle, which will re-publish).

### 6.9 Run Loop

- A long-running async task that repeatedly calls `scheduler_tick()`.
- If `scheduler_tick()` raises an unexpected exception, log it and sleep 1 second before retrying.
- `CancelledError` must propagate (for clean shutdown).

### 6.10 Lifecycle

- `start()`: Create and launch the run loop task (only if not already running).
- `stop()`: Cancel the run loop task and await its completion.

### 6.11 Queue Snapshot

A snapshot of the current queue state. It includes all non-Completed messages, sorted by:
1. Priority: high first (high → 0, normal → 1)
2. `display_count` ascending
3. `created_at` ascending

Returns a list of `message.to_dict()` objects.

### 6.12 SSE Subscriber System

- `subscribe_queue()`:
  - Create a queue (max size 500).
  - Seed with three initial events:
    1. `{"type": "current", "message": <current_snapshot or null>}`
    2. `{"type": "queue", "messages": <queue_snapshot>}`
    3. `{"type": "history", "messages": <history_snapshot>}`
  - Register the queue.
  - Return the queue.
- `unsubscribe_queue(queue)`: Remove from subscriber set.
- `_notify(event)`: Push event to all subscriber queues.
  - If a queue is full, drop the oldest event and retry once.

### 6.13 Public Accessors

| Method | Returns | Description |
|---|---|---|
| `get_active_messages()` | []Message | All non-Completed messages |
| `get_all_messages()` | []Message | All messages |
| `get_history()` | []dict | Copy of the history ring buffer |
| `get_current_message()` | Message or null | The currently-displayed message |
| `state()` | string | `"Active"` if current is set OR active messages exist; else `"Idle"` |
| `high_priority_count()` | int | Count of non-Completed messages with priority "high" |

---

## 7. HTTP API

### 7.1 Static File Serving

- Mount a static file server at `/static` serving files from `frontend/static/`.
- `GET /` → serve `frontend/static/index.html` as a file response.

### 7.2 GET /api/config

Returns the current configuration and connection status.

**Response** (200 OK, JSON):
```json
{
  "publish_topic": "splitflap/splitflap/set",
  "subscribe_topic": "splitflap/splitflap/state",
  "broker_host": "localhost",
  "connected": true,
  "default_display_duration": 10,
  "default_target_display_count": 6,
  "idle_message": "WELCOME",
  "idle_mode": "publish",
  "scheduler_enabled": true
}
```

### 7.3 POST /api/publish

Submit a new message to the scheduler queue.

**Request Body** (JSON):
```json
{
  "text": "HELLO",
  "payload": "HELLO",
  "target_display_count": 3,
  "display_duration": 10,
  "priority": "normal"
}
```

- `text` and `payload` are both optional; the server uses whichever is non-null/non-empty (preferring `text`). At least one must be provided and non-empty after stripping whitespace.
- `target_display_count`: optional int, defaults to configured value.
- `display_duration`: optional int, defaults to configured value.
- `priority`: optional string, `"normal"` (default) or `"high"`.

**User Extraction**: Read the `Cf-Access-Authenticated-User-Email` header. If present, extract the part before `@` as the username. If absent, use `"unknown"`.

**Response** (200 OK):
```json
{"status": "ok", "id": "uuid-string"}
```

**Error Responses**:
- 400: `"text must be non-empty"` if both text and payload are empty/null.
- 400: Validation errors from the scheduler (e.g., invalid target_display_count, display_duration, or priority).
- 503: `"scheduler not ready"` if the scheduler has not been initialized.

### 7.4 GET /api/messages/current

Returns the scheduler's current message (what the scheduler most recently published).

**Response** (200 OK):
- If a message is currently being displayed: the message's `to_dict()` JSON.
- If no message is current: `null`.

**Note**: This reflects the scheduler's internal state, NOT necessarily what the physical display is showing.

### 7.5 GET /api/messages/display-state

Returns the latest message received from the physical display via MQTT.

**Response** (200 OK):
- If a message has been received: `{"message": "<payload>"}`.
- If no message has been received yet: `null`.

### 7.6 DELETE /api/messages/{message_id}

Remove a queued message by its UUID.

- The message is marked Completed and will never be displayed again.
- This operates on the scheduler's queue, not the physical display.

**Response** (200 OK):
```json
{"status": "ok"}
```

**Error Responses**:
- 400: `"invalid uuid"` if the path parameter is not a valid UUID.
- 404: `"message not found"` if no message with that UUID exists.

### 7.7 GET /api/scheduler/status

Returns a summary of the scheduler's state.

**Response** (200 OK, JSON):
```json
{
  "state": "Active",
  "current": { ... } | null,
  "queueSize": 3,
  "highPriorityCount": 1
}
```

- `state`: `"Active"` or `"Idle"`.
- `current`: the current message's `to_dict()` or null.
- `queueSize`: count of active (non-Completed) messages.
- `highPriorityCount`: count of active high-priority messages.

### 7.8 GET /api/scheduler/stream (SSE)

A Server-Sent Events endpoint that streams real-time updates to the client.

**Event Types**:

The stream merges two event sources into a single SSE connection. Events are distinguished by the SSE `event` field.

#### Event: `current`
Emitted when the scheduler's current message changes.

```
event: current
data: {"type":"current","message":{...} | null}
```

The `message` field is the full `to_dict()` of the current message, or `null` when no message is current.

#### Event: `display-state`
Emitted when the physical display reports a new state via MQTT.

```
event: display-state
data: {"type":"display-state","message":{"message":"<payload>"}}
```

#### Event: `queue`
Emitted when the message queue changes (message added, removed, display_count updated, status changed).

```
event: queue
data: {"type":"queue","messages":[...]}
```

The `messages` array is a full snapshot of the current queue (non-Completed messages, sorted).

#### Event: `history`
Emitted when the message history changes (new message added).

```
event: history
data: {"type":"history","messages":[...]}
```

The `messages` array is a full snapshot of the history ring buffer (most recent first).

### 7.9 SSE Stream Implementation Details

The stream merges two internal event queues:
1. The scheduler's notification queue (produces `current`, `queue`, `history` events).
2. The MQTT client's display-state queue (produces `display-state` events).

**Merge Strategy**: Use a relay-based merge pattern:
- Create a shared merged queue (max size 1000).
- For each source queue, spawn a dedicated relay goroutine/task that reads from the source and writes to the merged queue.
- The SSE handler reads from the merged queue one event at a time.
- When the client disconnects (the generator/handler is closed), cancel all relay tasks.

This pattern avoids the task-leak and deadlock issues that can occur with `select`/`wait`-based approaches when one source has a burst of events while another is idle.

**Cleanup**: On client disconnect, unsubscribe from both the scheduler and MQTT client queues.

---

## 8. Application Lifecycle

### 8.1 Startup Sequence

1. Load configuration from `app.conf` and/or environment variables.
2. Log all configuration values.
3. Create the Scheduler instance with the MQTT client and configuration values.
4. Start the MQTT client background task (connects to broker, subscribes to state topic).
5. If `scheduler_enabled` is true, start the scheduler run loop.
6. Start the HTTP server on port 8100.

### 8.2 Shutdown Sequence

1. Stop the scheduler run loop (cancel task, await completion).
2. Stop the MQTT client (cancel task, await completion).
3. HTTP server stops.

---

## 9. Concurrency Model

The Python implementation uses `asyncio` (cooperative multitasking). The Go implementation should use goroutines and channels:

- The MQTT client runs in its own goroutine.
- The scheduler run loop runs in its own goroutine.
- The SSE merge uses dedicated relay goroutines per source queue.
- All shared state (MessageStore, scheduler internals, MQTT client state) must be protected by mutexes or accessed through channels to prevent data races.

---

## 10. Error Handling

### 10.1 MQTT Connection Errors

- Log and retry after 5 seconds indefinitely.
- The `publish()` method returns an error if not connected; the scheduler handles this gracefully.

### 10.2 Publish Errors During Tick

- If MQTT publish fails during a scheduler tick:
  - Do NOT increment `display_count`.
  - Do NOT set `last_displayed_at`.
  - Set `_current` to null.
  - Notify subscribers that current is null.
  - Sleep 1 second as backoff.
  - Return from the tick (the run loop will retry on the next iteration).

### 10.3 Publish Errors During Idle

- If MQTT publish fails during idle mode, silently ignore the error.

### 10.4 Scheduler Tick Errors

- If `scheduler_tick()` panics/returns an unexpected error, log it, sleep 1 second, and retry.

---

## 11. Health Check

The Docker health check hits `GET /api/config`. The endpoint must respond with 200 OK when the server is running.

---

## 12. Docker / Deployment

- The server listens on port 8100.
- The Dockerfile should build a Go binary and run it.
- The `app.conf` file should be copied into the container.
- The `frontend/static/` directory should be copied into the container.
- The binary should serve static files from the correct relative path.

---

## 13. Key Behavioral Invariants

These invariants MUST hold in the implementation:

1. **Publish failure does not increment display_count**: If MQTT publish fails, the message's `display_count` and `last_displayed_at` remain unchanged.

2. **Queue snapshot emitted before dwell**: After a successful publish and display_count increment, the queue snapshot SSE event is emitted BEFORE the display_duration sleep begins.

3. **Full display duration**: The scheduler always sleeps for the full `display_duration`. No early wake-up is permitted during the dwell period.

4. **Completion after full dwell**: A message is marked Completed only AFTER the full display_duration has elapsed AND display_count >= target_display_count.

5. **Priority dominates count**: A high-priority message with display_count=99 is selected over a normal-priority message with display_count=0.

6. **Lowest count wins among same priority**: Among messages of the same priority, the one with the lowest display_count is selected.

7. **Oldest wins on full tie**: Among messages with the same priority and display_count, the oldest (earliest created_at) is selected.

8. **Completed messages are never re-selected**: Once a message is Completed, it is excluded from selection permanently.

9. **Idle wakeup**: When a new message is added while the scheduler is idle, the `_wakeup` event is signaled, causing the idle handler to return promptly so the new message can be processed.

10. **SSE seed events**: When a new SSE client connects, it immediately receives the current state (current, queue, history snapshots) so it can render without waiting for the next change.

11. **Display-state vs current distinction**: The `current` event reflects what the scheduler last published. The `display-state` event reflects what the physical display reports via MQTT feedback. These are separate event types on the SSE stream.

12. **Raw string MQTT payload**: Messages published to MQTT are raw UTF-8 strings (the message text), NOT JSON-encoded.

13. **History ring buffer sizes**:
    - MQTT client history: max 500 entries.
    - Scheduler history: max 50 entries.

14. **SSE queue max sizes**:
    - Scheduler subscriber queue: 500.
    - MQTT display-state subscriber queue: 100.
    - SSE merge queue: 1000.
