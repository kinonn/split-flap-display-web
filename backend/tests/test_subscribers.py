"""Tests for the SSE subscriber/notification system on the Scheduler."""

import asyncio

from ._fixtures import AsyncTestCase


class SubscriberTests(AsyncTestCase):

    async def test_subscribe_creates_queue(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        try:
            self.assertIsInstance(q, asyncio.Queue)
        finally:
            s.unsubscribe_queue(q)

    async def test_unsubscribe_removes_queue(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        self.assertIn(q, s._subscribers)
        s.unsubscribe_queue(q)
        self.assertNotIn(q, s._subscribers)

    async def test_unsubscribe_unknown_queue_is_safe(self):
        s, _ = self._make_scheduler()
        q = asyncio.Queue()
        # Should not raise
        s.unsubscribe_queue(q)
        self.assertNotIn(q, s._subscribers)

    async def test_add_message_emits_queue_event(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        try:
            mid = await s.add_message("HELLO", priority="high")
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "queue")
            self.assertEqual(evt["message"]["id"], str(mid))
        finally:
            s.unsubscribe_queue(q)

    async def test_remove_message_emits_queue_event(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        try:
            mid = await s.add_message("HELLO")
            # Drain the add event
            await asyncio.wait_for(q.get(), timeout=0.5)
            await s.remove_message(mid)
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "queue")
            self.assertEqual(evt["removed"], str(mid))
        finally:
            s.unsubscribe_queue(q)

    async def test_current_event_emitted_during_tick(self):
        s, mqtt = self._make_scheduler(default_display_duration=0.02)
        q = s.subscribe_queue()
        try:
            await s.add_message("HELLO")
            # Drain the add event
            await asyncio.wait_for(q.get(), timeout=0.5)
            tick = asyncio.create_task(s.scheduler_tick())
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "current")
            self.assertEqual(evt["message"]["message"], "HELLO")
            # Let the tick finish cleanly
            await asyncio.wait_for(tick, timeout=1.0)
        finally:
            s.unsubscribe_queue(q)

    async def test_multiple_subscribers_each_receive_events(self):
        s, _ = self._make_scheduler()
        q1 = s.subscribe_queue()
        q2 = s.subscribe_queue()
        try:
            await s.add_message("HELLO")
            evt1 = await asyncio.wait_for(q1.get(), timeout=0.5)
            evt2 = await asyncio.wait_for(q2.get(), timeout=0.5)
            self.assertEqual(evt1["type"], "queue")
            self.assertEqual(evt2["type"], "queue")
        finally:
            s.unsubscribe_queue(q1)
            s.unsubscribe_queue(q2)

    async def test_unsubscribed_does_not_receive(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        s.unsubscribe_queue(q)
        await s.add_message("HELLO")
        self.assertTrue(q.empty())
