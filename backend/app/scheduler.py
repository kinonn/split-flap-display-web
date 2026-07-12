import asyncio
import logging
import uuid
from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum
from typing import TYPE_CHECKING, Literal, Optional

if TYPE_CHECKING:
    from .mqtt_client import MQTTClient


logger = logging.getLogger(__name__)


class MessageStatus(str, Enum):
    PENDING = "Pending"
    ACTIVE = "Active"
    COMPLETED = "Completed"


Priority = Literal["normal", "high"]
PRIORITY_RANK = {"normal": 0, "high": 1}


@dataclass
class Message:
    id: uuid.UUID
    message: str
    created_at: datetime
    status: MessageStatus
    display_duration: int
    target_display_count: int
    display_count: int
    last_displayed_at: Optional[datetime]
    priority: Priority = "normal"

    def to_dict(self) -> dict:
        return {
            "id": str(self.id),
            "message": self.message,
            "createdAt": self.created_at.isoformat(),
            "status": self.status.value,
            "displayDuration": self.display_duration,
            "targetDisplayCount": self.target_display_count,
            "displayCount": self.display_count,
            "lastDisplayedAt": self.last_displayed_at.isoformat() if self.last_displayed_at else None,
            "priority": self.priority,
        }


class MessageStore:
    def __init__(self) -> None:
        self._messages: dict[uuid.UUID, Message] = {}
        self._lock = asyncio.Lock()

    async def add(self, message: Message) -> None:
        async with self._lock:
            self._messages[message.id] = message

    async def get(self, message_id: uuid.UUID) -> Optional[Message]:
        async with self._lock:
            return self._messages.get(message_id)

    async def list_active(self) -> list[Message]:
        async with self._lock:
            return [m for m in self._messages.values() if m.status != MessageStatus.COMPLETED]

    async def list_all(self) -> list[Message]:
        async with self._lock:
            return list(self._messages.values())

    async def mark_completed(self, message_id: uuid.UUID) -> bool:
        async with self._lock:
            m = self._messages.get(message_id)
            if m is None:
                return False
            m.status = MessageStatus.COMPLETED
            return True

    async def update_fields(self, message_id: uuid.UUID, **kwargs) -> None:
        async with self._lock:
            m = self._messages.get(message_id)
            if m is None:
                return
            for k, v in kwargs.items():
                setattr(m, k, v)


class Scheduler:
    def __init__(
        self,
        mqtt: "MQTTClient",
        publish_topic: str,
        default_display_duration: int,
        default_target_display_count: int,
        idle_message: str,
        idle_mode: str,
        idle_publish_interval: int,
    ) -> None:
        self._mqtt = mqtt
        self._publish_topic = publish_topic
        self._default_display_duration = default_display_duration
        self._default_target_display_count = default_target_display_count
        self._idle_message = idle_message
        self._idle_mode = idle_mode
        self._idle_publish_interval = idle_publish_interval

        self._store = MessageStore()
        self._current: Optional[Message] = None
        self._lock = asyncio.Lock()
        self._wakeup = asyncio.Event()
        self._task: Optional[asyncio.Task] = None

        self._subscribers: set[asyncio.Queue] = set()

    # ---- Public API (spec §14) ----

    async def add_message(
        self,
        text: str,
        target_display_count: Optional[int] = None,
        display_duration: Optional[int] = None,
        priority: Priority = "normal",
    ) -> uuid.UUID:
        if not text or not text.strip():
            raise ValueError("text must be non-empty")

        target = target_display_count if target_display_count is not None else self._default_target_display_count
        duration = display_duration if display_duration is not None else self._default_display_duration

        if target <= 0:
            raise ValueError("target_display_count must be > 0")
        if duration <= 0:
            raise ValueError("display_duration must be > 0")
        if priority not in ("normal", "high"):
            raise ValueError("priority must be 'normal' or 'high'")

        msg = Message(
            id=uuid.uuid4(),
            message=text,
            created_at=datetime.now(),
            status=MessageStatus.PENDING,
            display_duration=duration,
            target_display_count=target,
            display_count=0,
            last_displayed_at=None,
            priority=priority,
        )
        await self._store.add(msg)
        self._wakeup.set()
        self._notify({"type": "queue", "message": msg.to_dict()})
        return msg.id

    async def remove_message(self, message_id: uuid.UUID) -> bool:
        ok = await self._store.mark_completed(message_id)
        if ok:
            self._notify({"type": "queue", "removed": str(message_id)})
        return ok

    def get_active_messages(self) -> list[Message]:
        return [m for m in self._store._messages.values() if m.status != MessageStatus.COMPLETED]

    def get_all_messages(self) -> list[Message]:
        return list(self._store._messages.values())

    def get_current_message(self) -> Optional[Message]:
        return self._current

    def select_next_message(self) -> Optional[Message]:
        candidates = [
            m for m in self._store._messages.values()
            if m.status != MessageStatus.COMPLETED
        ]
        if not candidates:
            return None
        return min(
            candidates,
            key=lambda m: (
                -PRIORITY_RANK[m.priority],
                m.display_count,
                m.created_at,
            ),
        )

    def state(self) -> str:
        if self._current is not None:
            return "Active"
        if self.get_active_messages():
            return "Active"
        return "Idle"

    def high_priority_count(self) -> int:
        return sum(1 for m in self.get_active_messages() if m.priority == "high")

    # ---- Lifecycle ----

    async def start(self) -> None:
        if self._task is None:
            self._task = asyncio.create_task(self._run_loop())

    async def stop(self) -> None:
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None

    async def _run_loop(self) -> None:
        while True:
            try:
                await self.scheduler_tick()
            except asyncio.CancelledError:
                raise
            except Exception:
                logger.exception("scheduler_tick failed")
                await asyncio.sleep(1)

    # ---- Tick (spec §14) ----

    async def scheduler_tick(self) -> None:
        next_msg = self.select_next_message()
        if next_msg is None:
            await self._handle_idle()
            return

        self._current = next_msg
        self._notify({"type": "current", "message": next_msg.to_dict()})

        try:
            await self._mqtt.publish(self._publish_topic, next_msg.message, qos=0)
        except RuntimeError as e:
            logger.warning("Publish failed; will retry next tick: %s", e)
            self._current = None
            self._notify({"type": "current", "message": None})
            await asyncio.sleep(1)
            return
        except Exception:
            logger.exception("Publish raised; will retry next tick")
            self._current = None
            self._notify({"type": "current", "message": None})
            await asyncio.sleep(1)
            return

        next_msg.last_displayed_at = datetime.now()
        self._notify({"type": "current", "message": next_msg.to_dict()})

        # Full displayDuration — no early wake (spec §9).
        await asyncio.sleep(next_msg.display_duration)

        async with self._lock:
            next_msg.display_count += 1
            if next_msg.display_count >= 1 and next_msg.status == MessageStatus.PENDING:
                next_msg.status = MessageStatus.ACTIVE
            completed = next_msg.display_count >= next_msg.target_display_count
            if completed:
                next_msg.status = MessageStatus.COMPLETED
                self._current = None

        self._notify({"type": "current", "message": self._current.to_dict() if self._current else None})
        self._notify({"type": "queue", "message": next_msg.to_dict()})

    async def _handle_idle(self) -> None:
        if self._current is not None:
            self._current = None
            self._notify({"type": "current", "message": None})

        if self._idle_mode == "keep":
            try:
                await asyncio.wait_for(self._wakeup.wait(), timeout=1.0)
                self._wakeup.clear()
            except asyncio.TimeoutError:
                pass
            return

        # "publish" mode
        try:
            await self._mqtt.publish(self._publish_topic, self._idle_message, qos=0)
        except (RuntimeError, Exception):
            pass

        try:
            await asyncio.wait_for(
                self._wakeup.wait(),
                timeout=self._idle_publish_interval,
            )
            self._wakeup.clear()
        except asyncio.TimeoutError:
            pass

    # ---- Subscribers (SSE) ----

    def subscribe_queue(self) -> asyncio.Queue:
        q: asyncio.Queue = asyncio.Queue(maxsize=500)
        self._subscribers.add(q)
        return q

    def unsubscribe_queue(self, q: asyncio.Queue) -> None:
        self._subscribers.discard(q)

    def _notify(self, event: dict) -> None:
        for q in list(self._subscribers):
            try:
                q.put_nowait(event)
            except asyncio.QueueFull:
                try:
                    q.get_nowait()
                except asyncio.QueueEmpty:
                    pass
                try:
                    q.put_nowait(event)
                except asyncio.QueueFull:
                    pass
