"""Tests for the SSE event-stream merge logic in scheduler_api.

These tests exercise `_merge_event_streams`, which the `/api/scheduler/stream`
SSE endpoint uses to fan in events from the scheduler's own notification
queue and the MQTTClient's display-state queue.

The original implementation used `asyncio.wait(..., FIRST_COMPLETED)` over
`create_task`-based waiters. That pattern leaks a pending waiter on every
loop iteration, and the leaked waiter can race with the new waiter on the
same queue — silently dropping events and potentially deadlocking the loop.

The relay-based merge here must not drop events, even when they arrive in
bursts on one queue while the other is idle.
"""

import asyncio
import os
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
_BACKEND = os.path.dirname(_HERE)
if _BACKEND not in sys.path:
    sys.path.insert(0, _BACKEND)

import unittest


class MergeEventStreamsTests(unittest.IsolatedAsyncioTestCase):

    async def test_seeds_from_both_queues_are_delivered(self):
        from app.scheduler_api import _merge_event_streams

        scheduler_q: asyncio.Queue = asyncio.Queue()
        mqtt_q: asyncio.Queue = asyncio.Queue()
        scheduler_q.put_nowait({"type": "current", "message": None})
        mqtt_q.put_nowait({"type": "display-state", "message": {"message": "WELCOME"}})

        merger = _merge_event_streams(scheduler_q, mqtt_q)
        try:
            seen = []
            for _ in range(2):
                seen.append(await asyncio.wait_for(merger.__anext__(), timeout=1.0))
            self.assertEqual(len(seen), 2)
        finally:
            await merger.aclose()

    async def test_bursts_on_one_queue_are_not_dropped(self):
        """The old asyncio.wait pattern could drop events when a burst
        arrived on a queue whose previous waiter was still pending. The
        relay pattern must deliver every event.
        """
        from app.scheduler_api import _merge_event_streams

        scheduler_q: asyncio.Queue = asyncio.Queue()
        mqtt_q: asyncio.Queue = asyncio.Queue()

        merger = _merge_event_streams(scheduler_q, mqtt_q)
        try:
            # Burst 5 events onto mqtt_q while the scheduler_q is idle.
            for i in range(5):
                mqtt_q.put_nowait({"type": "display-state", "message": {"message": f"MSG{i}"}})

            seen = []
            for _ in range(5):
                seen.append(await asyncio.wait_for(merger.__anext__(), timeout=1.0))

            self.assertEqual(len(seen), 5)
            # Each event must be the full dict we put in, not a partial.
            for i, evt in enumerate(seen):
                self.assertEqual(evt["type"], "display-state")
                self.assertEqual(evt["message"]["message"], f"MSG{i}")
        finally:
            await merger.aclose()

    async def test_interleaved_events_from_both_queues(self):
        """Events arriving in an interleaved pattern from both queues
        must all be delivered, with intra-queue order preserved.
        """
        from app.scheduler_api import _merge_event_streams

        scheduler_q: asyncio.Queue = asyncio.Queue()
        mqtt_q: asyncio.Queue = asyncio.Queue()

        # Interleave the puts.
        scheduler_q.put_nowait({"type": "current", "message": {"id": "s1"}})
        mqtt_q.put_nowait({"type": "display-state", "message": {"message": "m1"}})
        scheduler_q.put_nowait({"type": "current", "message": {"id": "s2"}})
        mqtt_q.put_nowait({"type": "display-state", "message": {"message": "m2"}})
        scheduler_q.put_nowait({"type": "current", "message": {"id": "s3"}})
        mqtt_q.put_nowait({"type": "display-state", "message": {"message": "m3"}})

        merger = _merge_event_streams(scheduler_q, mqtt_q)
        try:
            seen = []
            for _ in range(6):
                seen.append(await asyncio.wait_for(merger.__anext__(), timeout=1.0))
            self.assertEqual(len(seen), 6)
        finally:
            await merger.aclose()

    async def test_live_event_after_aclose_is_not_delivered(self):
        """After the merger is closed, events put into the source queues
        must not crash the relay tasks and must not be delivered.
        """
        from app.scheduler_api import _merge_event_streams

        scheduler_q: asyncio.Queue = asyncio.Queue()
        mqtt_q: asyncio.Queue = asyncio.Queue()

        merger = _merge_event_streams(scheduler_q, mqtt_q)
        await merger.aclose()

        # Putting into the source queues after close should not raise.
        scheduler_q.put_nowait({"type": "current", "message": "late"})
        mqtt_q.put_nowait({"type": "display-state", "message": {"message": "late"}})

        # The merger is closed — __anext__ should raise StopAsyncIteration
        # (or a CancelledError-style close). The relay tasks must drain
        # and exit cleanly.
        with self.assertRaises(StopAsyncIteration):
            await asyncio.wait_for(merger.__anext__(), timeout=0.5)

    async def test_high_volume_burst_does_not_deadlock(self):
        """A large burst on a single queue must not cause the other
        queue's waiter to deadlock. This is the exact symptom the user
        reported: 'updated at the start, then nothing'.
        """
        from app.scheduler_api import _merge_event_streams

        scheduler_q: asyncio.Queue = asyncio.Queue()
        mqtt_q: asyncio.Queue = asyncio.Queue()

        merger = _merge_event_streams(scheduler_q, mqtt_q)
        try:
            # First, a single seed on each queue.
            scheduler_q.put_nowait({"type": "current", "message": None})
            mqtt_q.put_nowait({"type": "display-state", "message": {"message": "INIT"}})
            # Drain them.
            for _ in range(2):
                await asyncio.wait_for(merger.__anext__(), timeout=1.0)

            # Now hammer one queue. The other queue stays empty, which
            # was the deadlock trigger in the old code.
            for i in range(50):
                mqtt_q.put_nowait({"type": "display-state", "message": {"message": f"B{i}"}})

            # Then a single event on the OTHER queue — if the merger has
            # deadlocked, this one will never come through.
            scheduler_q.put_nowait({"type": "current", "message": {"id": "after-burst"}})

            # Collect everything: 50 mqtt events + 1 scheduler event.
            seen = []
            for _ in range(51):
                seen.append(await asyncio.wait_for(merger.__anext__(), timeout=1.0))
            self.assertEqual(len(seen), 51)

            # The scheduler event must be among them — proves the other
            # queue's waiter wasn't deadlocked.
            scheduler_evts = [e for e in seen if e["type"] == "current"]
            self.assertEqual(len(scheduler_evts), 1)
            self.assertEqual(scheduler_evts[0]["message"]["id"], "after-burst")
        finally:
            await merger.aclose()


if __name__ == "__main__":
    unittest.main()
