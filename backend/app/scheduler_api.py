import asyncio
import json
import logging
import uuid
from typing import Optional

from fastapi import APIRouter, HTTPException
from sse_starlette.sse import EventSourceResponse

from .mqtt_client import mqtt_client
from .scheduler import Scheduler


logger = logging.getLogger(__name__)


router = APIRouter()


_scheduler: Optional[Scheduler] = None


def set_scheduler(s: Scheduler) -> None:
    global _scheduler
    _scheduler = s


def _get() -> Scheduler:
    if _scheduler is None:
        raise RuntimeError("Scheduler not initialized")
    return _scheduler


@router.get("/api/messages/current")
async def current():
    m = _get().get_current_message()
    return m.to_dict() if m else None


@router.get("/api/messages/display-state")
async def display_state():
    """Return the latest message from the display firmware (MQTT retained state topic).

    This reflects what the physical display is actually showing, which persists
    until the next write — unlike /api/messages/current which clears when the
    scheduler's message completes its display cycle.
    """
    from .mqtt_client import mqtt_client
    latest = mqtt_client.get_latest_message()
    if latest is None:
        return None
    return {"message": latest}


@router.delete("/api/messages/{message_id}")
async def remove(message_id: str):
    try:
        uid = uuid.UUID(message_id)
    except ValueError:
        raise HTTPException(status_code=400, detail="invalid uuid")
    ok = await _get().remove_message(uid)
    if not ok:
        raise HTTPException(status_code=404, detail="message not found")
    return {"status": "ok"}


@router.get("/api/scheduler/status")
async def status():
    s = _get()
    return {
        "state": s.state(),
        "current": s.get_current_message().to_dict() if s.get_current_message() else None,
        "queueSize": len(s.get_active_messages()),
        "highPriorityCount": s.high_priority_count(),
    }


async def _merge_event_streams(*sources: asyncio.Queue) -> "asyncio.AsyncIterator[dict]":
    """Merge multiple event queues into a single async iterator.

    Each source queue is drained by a dedicated long-lived relay task that
    copies events into a shared `merged` queue. The caller consumes the
    merged queue one event at a time. This avoids the task-leak race
    condition that `asyncio.wait(..., FIRST_COMPLETED)` over
    `create_task`-based waiters can produce: a pending waiter from a prior
    loop iteration can steal an event intended for a fresh waiter on the
    same queue, causing the new waiter to deadlock and events to be
    silently dropped.

    The relay tasks are cancelled when this generator is closed.
    """
    merged: asyncio.Queue = asyncio.Queue(maxsize=1000)
    relay_tasks: list[asyncio.Task] = []

    async def _relay(src: asyncio.Queue) -> None:
        try:
            while True:
                evt = await src.get()
                await merged.put(evt)
        except asyncio.CancelledError:
            return

    for src in sources:
        relay_tasks.append(asyncio.create_task(_relay(src)))

    try:
        while True:
            yield await merged.get()
    finally:
        for t in relay_tasks:
            t.cancel()


@router.get("/api/scheduler/stream")
async def stream():
    scheduler_q = _get().subscribe_queue()
    mqtt_q = mqtt_client.subscribe_display_state()

    async def event_generator():
        try:
            # The two event streams are kept separate on the wire (different
            # SSE event names) so the client can distinguish
            # scheduler-published "current" messages from display-reported
            # "display-state" messages.
            async for evt in _merge_event_streams(scheduler_q, mqtt_q):
                yield {"event": evt.get("type", "message"), "data": json.dumps(evt)}
        finally:
            _get().unsubscribe_queue(scheduler_q)
            mqtt_client.unsubscribe_display_state(mqtt_q)

    return EventSourceResponse(event_generator())
