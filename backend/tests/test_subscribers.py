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
            # Drain the seeded snapshots (current, queue, history).
            for _ in range(3):
                await asyncio.wait_for(q.get(), timeout=0.5)
            mid = await s.add_message("HELLO", priority="high")
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "queue")
            ids = [m["id"] for m in evt["messages"]]
            self.assertIn(str(mid), ids)
            self.assertEqual(evt["messages"][0]["priority"], "high")
        finally:
            s.unsubscribe_queue(q)

    async def test_add_message_emits_history_event(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        try:
            mid = await s.add_message("HELLO", priority="normal", user="alice")
            # Drain seed (3) + add events (queue + history = 2) = 5 total.
            events = []
            for _ in range(5):
                events.append(await asyncio.wait_for(q.get(), timeout=0.5))
            # Find the history event that contains the new message; the
            # seeded history event is empty and must be ignored.
            history_evt = next(
                (e for e in events
                 if e["type"] == "history" and e["messages"]),
                None,
            )
            self.assertIsNotNone(history_evt)
            self.assertEqual(history_evt["messages"][0]["id"], str(mid))
            self.assertEqual(history_evt["messages"][0]["user"], "alice")
            self.assertEqual(history_evt["messages"][0]["message"], "HELLO")
        finally:
            s.unsubscribe_queue(q)

    async def test_remove_message_emits_queue_event(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        try:
            # Drain the seeded snapshots.
            for _ in range(3):
                await asyncio.wait_for(q.get(), timeout=0.5)
            mid = await s.add_message("HELLO")
            # Drain the two add events (queue + history).
            for _ in range(2):
                await asyncio.wait_for(q.get(), timeout=0.5)
            await s.remove_message(mid)
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "queue")
            ids = [m["id"] for m in evt["messages"]]
            self.assertNotIn(str(mid), ids)
        finally:
            s.unsubscribe_queue(q)

    async def test_subscribe_seeds_initial_snapshots(self):
        s, _ = self._make_scheduler()
        await s.add_message("HELLO", priority="normal", user="bob")
        q = s.subscribe_queue()
        try:
            seen = {"current": None, "queue": None, "history": None}
            for _ in range(3):
                evt = await asyncio.wait_for(q.get(), timeout=0.5)
                if evt["type"] in seen:
                    seen[evt["type"]] = evt
            self.assertIsNotNone(seen["current"])
            self.assertIsNotNone(seen["queue"])
            self.assertIsNotNone(seen["history"])
            self.assertEqual(len(seen["queue"]["messages"]), 1)
            self.assertEqual(seen["queue"]["messages"][0]["message"], "HELLO")
            self.assertEqual(seen["history"]["messages"][0]["user"], "bob")
        finally:
            s.unsubscribe_queue(q)

    async def test_current_event_emitted_during_tick(self):
        s, mqtt = self._make_scheduler(default_display_duration=0.02)
        q = s.subscribe_queue()
        try:
            # Drain the seed snapshots.
            for _ in range(3):
                await asyncio.wait_for(q.get(), timeout=0.5)
            await s.add_message("HELLO")
            # Drain the add events (queue + history).
            for _ in range(2):
                await asyncio.wait_for(q.get(), timeout=0.5)
            tick = asyncio.create_task(s.scheduler_tick())
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "current")
            self.assertEqual(evt["message"]["message"], "HELLO")
            await asyncio.wait_for(tick, timeout=1.0)
        finally:
            s.unsubscribe_queue(q)

    async def test_multiple_subscribers_each_receive_events(self):
        s, _ = self._make_scheduler()
        q1 = s.subscribe_queue()
        q2 = s.subscribe_queue()
        try:
            # Both queues were seeded; drain them.
            for _ in range(3):
                await asyncio.wait_for(q1.get(), timeout=0.5)
                await asyncio.wait_for(q2.get(), timeout=0.5)
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
        # The queue was seeded but the subscriber was removed before
        # any new events were generated. The seeded events were placed
        # into the queue before unsubscribe, so we only assert that no
        # event arrived AFTER unsubscribe by checking that the most
        # recent event (if any) is one of the seed events.
        # The point of this test is that subsequent notifications must
        # not be delivered. Seed events placed before unsubscribe are
        # expected; we therefore only check that the queue is not
        # growing indefinitely and that the *latest* add had no effect.
        # A simpler check: the last seen event must not reference HELLO
        # unless it was a seed.
        if not q.empty():
            # drain remaining seed events; HELLO must not appear
            drained = []
            while not q.empty():
                drained.append(q.get_nowait())
            for evt in drained:
                payload = evt.get("messages")
                if payload:
                    self.assertNotIn("HELLO", [m.get("message") for m in payload])
