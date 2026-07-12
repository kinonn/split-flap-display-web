"""Shared test fixtures and helpers."""

import asyncio
import os
import sys
import time
import unittest

# Ensure the backend/app package is importable when running from the repo root
# or from the tests/ directory.
_HERE = os.path.dirname(os.path.abspath(__file__))
_BACKEND = os.path.dirname(_HERE)
if _BACKEND not in sys.path:
    sys.path.insert(0, _BACKEND)


class FakeMQTT:
    """A drop-in stand-in for the real MQTTClient used by Scheduler.

    Records every publish so tests can assert on the call sequence.
    Set `fail_n_publishes` to make the next N publishes raise RuntimeError,
    simulating broker errors.
    """

    def __init__(self):
        self.published: list[tuple[str, str, int]] = []
        self.fail_n_publishes = 0
        self.publish_call_count = 0

    async def publish(self, topic: str, payload: str, qos: int = 0) -> None:
        self.publish_call_count += 1
        if self.fail_n_publishes > 0:
            self.fail_n_publishes -= 1
            raise RuntimeError("simulated mqtt failure")
        self.published.append((topic, payload, qos))


class AsyncTestCase(unittest.IsolatedAsyncioTestCase):
    """Base for async unit tests.

    Subclasses can call `await self._make_scheduler(...)` to get a Scheduler
    wired to a FakeMQTT and standard defaults.
    """

    DEFAULT_KWARGS = dict(
        default_display_duration=10,
        default_target_display_count=3,
        idle_message="IDLE",
        idle_mode="publish",
        idle_publish_interval=10,
    )

    def _make_scheduler(self, mqtt=None, **overrides):
        from app.scheduler import Scheduler
        if mqtt is None:
            mqtt = FakeMQTT()
        kwargs = {**self.DEFAULT_KWARGS, **overrides}
        return Scheduler(mqtt=mqtt, publish_topic="t", **kwargs), mqtt


def make_message(**overrides):
    """Build a Message with sensible defaults; override any field by name."""
    import uuid
    from datetime import datetime
    from app.scheduler import Message, MessageStatus

    defaults = dict(
        id=uuid.uuid4(),
        message="hello",
        created_at=datetime.now(),
        status=MessageStatus.PENDING,
        display_duration=10,
        target_display_count=3,
        display_count=0,
        last_displayed_at=None,
        priority="normal",
    )
    defaults.update(overrides)
    return Message(**defaults)


def run_until(predicate, timeout: float = 1.0, interval: float = 0.01) -> bool:
    """Spin the event loop until `predicate()` is truthy or `timeout` elapses.

    Returns True if predicate became true, False on timeout.
    """
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(interval)
    return False
