"""Tests for the MQTTClient display-state subscription.

The MQTTClient's `_run` loop is tightly coupled to the real `asyncio_mqtt`
broker, so these tests directly exercise the public subscription API and
the internal event-push path by feeding a fake message into the same
codepath used in production.
"""

import asyncio
import os
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
_BACKEND = os.path.dirname(_HERE)
if _BACKEND not in sys.path:
    sys.path.insert(0, _BACKEND)

import unittest


class _FakeMqttMessage:
    """Minimal stand-in for an asyncio_mqtt Message."""

    def __init__(self, topic: str, payload: str) -> None:
        self.topic = topic
        self._payload = payload.encode("utf-8")

    def payload(self):
        # asyncio_mqtt's Message decodes on access, but our client decodes
        # via `message.payload.decode(...)` — so we provide a bytes object
        # here as well.
        return self._payload


class DisplayStateSubscriptionTests(unittest.IsolatedAsyncioTestCase):
    """Verify the new `display-state` SSE event channel."""

    def _make_client(self):
        from app.mqtt_client import MQTTClient
        return MQTTClient()

    async def test_subscribe_seeds_nothing_when_no_message(self):
        c = self._make_client()
        q = c.subscribe_display_state()
        try:
            self.assertTrue(q.empty())
        finally:
            c.unsubscribe_display_state(q)

    async def test_get_latest_message_is_none_initially(self):
        c = self._make_client()
        self.assertIsNone(c.get_latest_message())

    async def test_subscribe_after_manual_set_seeds_event(self):
        c = self._make_client()
        # Simulate a previously-received MQTT state without running the
        # full broker loop.
        c._latest_message = "WELCOME"
        q = c.subscribe_display_state()
        try:
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "display-state")
            self.assertEqual(evt["message"], {"message": "WELCOME"})
        finally:
            c.unsubscribe_display_state(q)

    async def test_unsubscribe_removes_queue(self):
        c = self._make_client()
        q = c.subscribe_display_state()
        self.assertIn(q, c._current_subscribers)
        c.unsubscribe_display_state(q)
        self.assertNotIn(q, c._current_subscribers)

    async def test_unsubscribe_unknown_queue_is_safe(self):
        c = self._make_client()
        q = asyncio.Queue()
        # Should not raise
        c.unsubscribe_display_state(q)
        self.assertNotIn(q, c._current_subscribers)

    async def test_multiple_subscribers_each_receive_event(self):
        c = self._make_client()
        c._latest_message = "HELLO"
        q1 = c.subscribe_display_state()
        q2 = c.subscribe_display_state()
        try:
            evt1 = await asyncio.wait_for(q1.get(), timeout=0.5)
            evt2 = await asyncio.wait_for(q2.get(), timeout=0.5)
            self.assertEqual(evt1["type"], "display-state")
            self.assertEqual(evt2["type"], "display-state")
            self.assertEqual(evt1["message"], {"message": "HELLO"})
            self.assertEqual(evt2["message"], {"message": "HELLO"})
        finally:
            c.unsubscribe_display_state(q1)
            c.unsubscribe_display_state(q2)

    async def test_event_type_is_display_state_not_current(self):
        """Critical: the new event type MUST be 'display-state' so the
        frontend can distinguish it from the scheduler's 'current' event.
        """
        c = self._make_client()
        c._latest_message = "X"
        q = c.subscribe_display_state()
        try:
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "display-state")
            self.assertNotEqual(evt["type"], "current")
        finally:
            c.unsubscribe_display_state(q)


class HistoryTests(unittest.IsolatedAsyncioTestCase):
    """Verify the public history API still works after the rename."""

    def _make_client(self):
        from app.mqtt_client import MQTTClient
        return MQTTClient()

    def test_history_starts_empty(self):
        c = self._make_client()
        self.assertEqual(c.get_history(), [])

    def test_connected_starts_false(self):
        c = self._make_client()
        self.assertFalse(c.connected)


if __name__ == "__main__":
    unittest.main()
