# Bugs & Audit Findings

Original verification date: 2026-08-19 (Go 1.26.5, windows/amd64)
Audit + fix verification date: 2026-08-30 (Go 1.22.12, linux/amd64, branch `fix/audit-issues`, commit `c894122`)

---

## Part 1 — Original bugs (all FIXED as of 2026-08-30)

### 1. Data race on message state — FIXED ✅
- File: `backend-go/internal/scheduler/scheduler.go`, `internal/models/message.go`
- Original issue: the scheduler tick mutated `DisplayCount`, `Status`, and
  `LastDisplayedAt` on live `*Message` pointers outside any lock while HTTP
  handlers and SSE subscribers read them concurrently via `ToDTO()`.
- Fix: `models.Message` now guards mutable fields with `sync.RWMutex`; all
  writes go through locked methods (`MarkDisplayed`, `MarkCompleted`,
  `SetDisplayCount`), all reads through locked accessors (`ToDTO`,
  `StatusSafe`, `DisplayCountSafe`, `IsCompleted`).
- Verification: `go test -race ./...` passes (cgo/gcc available on this
  machine; the original "no cgo" limitation no longer applies).

### 2. Scheduler.Stop() deadlock when Start never called — FIXED ✅
- File: `backend-go/internal/scheduler/scheduler.go`
- Fix: `Stop()` returns early when `running` is false; idempotent.

### 3. mqttclient.Start() double-call panic / Stop() hang — FIXED ✅
- File: `backend-go/internal/mqttclient/client.go`
- Fix: `Start()` guards on `running` under `lifecycleMu` and re-creates
  `done`; `Stop()` is guarded and idempotent. No `close of closed channel`
  path remains.

### 4. Static assets 404 in dev flow — FIXED ✅
- File: `backend-go/cmd/server/main.go`
- Fix: `findStaticDir()` includes `../frontend/static`, covering the
  documented `cd backend-go && go run ./cmd/server` flow.

---

## Part 2 — Audit findings FIXED in commit `c894122`

### C1. Graceful shutdown hung forever with active SSE clients (critical) — FIXED ✅
- Files: `backend-go/cmd/server/main.go`, `internal/api/server.go`
- Issue: `WriteTimeout: 0` + Fiber's `Shutdown()` waits indefinitely for
  live connections; SSE streams are long-lived and had no shutdown-cancel,
  so the process never exited on SIGTERM while a browser tab was open.
- Fix: active SSE stream contexts are tracked in `Server` (`sseCancels`)
  and cancelled by `Server.Shutdown()` before `ShutdownWithTimeout(5s)`;
  the stream writer also selects on its cancellation context.
- Verification: runtime test — SIGTERM with an active SSE client exits
  with code 0 in ~1s, "shutdown signal received"/"stopped" logged, port
  closed. (Previously: hang until SIGKILL.)

### C2. Malformed POST body silently wiped the display (critical) — FIXED ✅
- File: `backend-go/internal/api/server.go` (`handlePublish`)
- Issue: `BodyParser` errors were discarded, so any unparseable body
  queued an empty message (blank display) and returned 200 OK.
- Fix: non-empty unparseable bodies return 400 `invalid JSON body`;
  empty bodies are still tolerated (blank display is a valid request);
  `text/plain` bodies are used verbatim as the message text.
- Verification: runtime — malformed JSON → 400, raw text → 200,
  empty body → 200, valid JSON → 200.

### H1. mqttclient.Publish nil-pointer/race on c.client (high) — FIXED ✅
- File: `backend-go/internal/mqttclient/client.go`
- Fix: `Publish` snapshots the client under `lifecycleMu` and returns
  "not connected" when nil.

### H2. padToWidth used byte length instead of runes (high) — FIXED ✅
- File: `backend-go/internal/scheduler/scheduler.go`
- Fix: width math uses `utf8.RuneCountInString`; non-ASCII text now pads
  correctly for character-module displays.

### H3. Unbounded memory growth (high) — FIXED ✅
- Files: `internal/scheduler/scheduler.go`, `internal/store/store.go`,
  `internal/api/server.go`
- Fix: text capped at 256 runes (400 beyond), max 100 active messages
  (429 `queue is full` via new `QueueFullError`), and the store evicts
  the oldest Completed messages beyond 200 on every add.

### H4. Frontend caret forced to end on every keystroke (high) — FIXED ✅
- File: `frontend/static/app.js` (`renderInputText`)
- Fix: the DOM is only rebuilt when the filtered value actually changed,
  and the caret character offset is captured before the rebuild and
  restored after. Mid-string editing, IME composition, and native undo
  work again.

### H5. docker compose up failed on fresh clone (high) — FIXED ✅
- File: `docker-compose.yml`
- Fix: `env_file` entry for the git-ignored `backend-go/app.conf` is now
  optional (`path:`/`required: false`, Compose >= 2.24).

### Also fixed in the same commit
- `log.Fatalf` in the HTTP-server goroutine replaced with `log.Printf`
  (Fatalf skipped deferred cleanup and any in-flight shutdown).
- SSE relay/subscriber leak when the stream writer never runs: a
  writer-started flag plus a 30s fallback cancels the relays and drops
  the subscriptions. Note: the fasthttp `RequestCtx` must never be
  touched from a detached goroutine (`c.Context().Done()` panics after
  fasthttp releases the context — found and corrected during smoke
  testing).

---

## Part 3 — Open issues (Medium/Low, not yet fixed)

### Backend
- **M2. Seed-then-register TOCTOU on subscribe** (`mqttclient/client.go`,
  `scheduler.go`): subscribers snapshot state, seed the queue, then
  register — an event arriving in between is silently missed. Fix:
  register first under the producer's lock, then seed (dedupe one
  possible duplicate).
- **M3. Spoofable user attribution** (`api/server.go`): the
  `Cf-Access-Authenticated-User-Email` header is trusted unconditionally.
  Make it configurable or verify the CF Access JWT.
- **M4. Deleted current message still reported as current** (`scheduler.go`):
  deleting the message being displayed leaves `s.current` pointing at it
  until its dwell ends. Clear current / emit `current: null` on delete.
- **M5. Invalid config values silently ignored** (`config.go`): parse
  failures fall back to defaults with no warning; no range validation
  (e.g. `DEFAULT_DISPLAY_DURATION=0` causes a busy publish loop).
- **Low:** `handleConfig` exposes broker host; missing 503
  "scheduler not ready" path from spec §7.3; merged SSE queue can drop
  seed events under burst; dead code (`ErrInvalidInput`, `queue.Drain`).

### Frontend
- **M1. SSE reconnect has no duplicate-timer guard or backoff**
  (`app.js` `connectSchedulerSSE`): rapid error cycles can stack parallel
  EventSource connections; fixed 3s retry hammers a restarting server.
- **M2. `body` flex centering clips content on short viewports**
  (`style.css`): use `align-items: safe center` or margin-based centering.
- **M3. `removeMessage` has no in-flight guard**: double-click sends two
  DELETEs; the second surfaces a spurious "message not found" error.
- **M4. `showStatus`/`updateCharCount` fight over the same element**,
  leaving stale error/success styling.
- **Low:** ~80 lines of dead CSS (`#display-text`, `.display-line`,
  `.history-table`); invalid `autocomplete="off"` on a contenteditable
  div.

### Deployment
- **M5. No root `.dockerignore`**: the build context is the repo root,
  so `.git` and everything else is sent to the daemon on every build.
- **Low:** `/etc/localtime` bind mount is Linux-only; duplicate
  healthcheck definitions (Dockerfile vs compose) can drift; spec default
  `DEFAULT_TARGET_DISPLAY_COUNT` (6) disagrees with example config (3).

---

## Summary

| # | Issue | Severity | Status |
|---|-------|----------|--------|
| 1 | Data race on message state | critical | FIXED (verified with -race) |
| 2 | Scheduler.Stop() deadlock | medium | FIXED |
| 3 | mqttclient double Start panic | medium | FIXED |
| 4 | Static assets 404 in dev flow | medium | FIXED |
| C1 | Shutdown hang with SSE clients | critical | FIXED (runtime-verified) |
| C2 | Malformed body wipes display | critical | FIXED (runtime-verified) |
| H1 | Publish nil-pointer/race | high | FIXED |
| H2 | Byte-based padding | high | FIXED |
| H3 | Unbounded memory growth | high | FIXED |
| H4 | Caret forced to end | high | FIXED |
| H5 | compose env_file hard fail | high | FIXED |
| M2–M5 (backend), M1–M4 (frontend), M5 (deploy) | see Part 3 | medium | OPEN |
| Low items | see Part 3 | low | OPEN |
