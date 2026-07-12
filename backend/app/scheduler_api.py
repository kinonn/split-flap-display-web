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


@router.get("/api/scheduler/stream")
async def stream():
    scheduler_q = _get().subscribe_queue()
    mqtt_q = mqtt_client.subscribe_current()

    async def event_generator():
        try:
            while True:
                # Wait for events from either the scheduler queue or the MQTT current queue
                done, _ = await asyncio.wait(
                    [
                        asyncio.create_task(scheduler_q.get()),
                        asyncio.create_task(mqtt_q.get()),
                    ],
                    return_when=asyncio.FIRST_COMPLETED,
                )
                for task in done:
                    evt = task.result()
                    yield {"event": evt.get("type", "message"), "data": json.dumps(evt)}
        except asyncio.CancelledError:
            _get().unsubscribe_queue(scheduler_q)
            mqtt_client.unsubscribe_current(mqtt_q)
            raise

    return EventSourceResponse(event_generator())
