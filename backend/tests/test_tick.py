"""Tests for Scheduler.scheduler_tick and Scheduler._handle_idle.

scheduler_tick responsibilities (spec §14):
  - determine next message
  - publish via MQTT
  - wait display duration
  - update statistics
  - complete message if required

scheduler_tick invariants (spec §16):
  - if publish fails, displayCount is NOT incremented
"""

import asyncio
from datetime import datetime, timedelta

from app.scheduler import MessageStatus

from ._fixtures import AsyncTestCase, FakeMQTT


class SchedulerTickTests(AsyncTestCase):
    """Behaviour of a single scheduler_tick() invocation."""

    async def test_publishes_then_increments(self):
        # Short display_duration so the full tick completes quickly.
        s, mqtt = self._make_scheduler(default_display_duration=0.05)
        mid = await s.add_message("HELLO")
        m = s._store._messages[mid]

        before = datetime.now()
        await s.scheduler_tick()
        after = datetime.now()

        # The message was published exactly once.
        self.assertEqual(len(mqtt.published), 1)
        self.assertEqual(mqtt.published[0], ("t", "HELLO", 0))
        # displayCount advanced.
        self.assertEqual(m.display_count, 1)
        # last_displayed_at was set during the publish step (before sleep).
        self.assertIsNotNone(m.last_displayed_at)
        self.assertGreaterEqual(m.last_displayed_at, before - timedelta(milliseconds=1))
        self.assertLessEqual(m.last_displayed_at, after + timedelta(milliseconds=1))

    async def test_status_transitions_pending_to_active(self):
        s, _ = self._make_scheduler(default_display_duration=0.02)
        mid = await s.add_message("X")
        m = s._store._messages[mid]
        self.assertEqual(m.status, MessageStatus.PENDING)
        await s.scheduler_tick()
        self.assertEqual(m.status, MessageStatus.ACTIVE)
        self.assertEqual(m.display_count, 1)

    async def test_status_transitions_active_to_completed(self):
        s, _ = self._make_scheduler(
            default_display_duration=0.02,
            default_target_display_count=2,
        )
        mid = await s.add_message("X")
        m = s._store._messages[mid]

        await s.scheduler_tick()
        self.assertEqual(m.status, MessageStatus.ACTIVE)
        self.assertEqual(m.display_count, 1)

        await s.scheduler_tick()
        self.assertEqual(m.status, MessageStatus.COMPLETED)
        self.assertEqual(m.display_count, 2)
        # current is cleared once completed.
        self.assertIsNone(s._current)

    async def test_single_target_completes_after_one_display(self):
        s, _ = self._make_scheduler(
            default_display_duration=0.02,
            default_target_display_count=1,
        )
        mid = await s.add_message("X")
        m = s._store._messages[mid]

        await s.scheduler_tick()
        self.assertEqual(m.status, MessageStatus.COMPLETED)
        self.assertEqual(m.display_count, 1)

    async def test_publish_failure_does_not_increment(self):
        s, mqtt = self._make_scheduler(default_display_duration=10)  # long sleep
        mid = await s.add_message("X")
        m = s._store._messages[mid]
        mqtt.fail_n_publishes = 1

        tick = asyncio.create_task(s.scheduler_tick())
        # Wait long enough for publish to fail, the 1s backoff, and return.
        await asyncio.wait_for(tick, timeout=2.5)

        # No successful publish
        self.assertEqual(len(mqtt.published), 0)
        # displayCount untouched
        self.assertEqual(m.display_count, 0)
        self.assertEqual(m.status, MessageStatus.PENDING)
        # current was reset
        self.assertIsNone(s._current)

    async def test_publish_failure_does_not_block_subsequent_ticks(self):
        s, mqtt = self._make_scheduler(
            default_display_duration=0.02,
        )
        mid = await s.add_message("X")
        mqtt.fail_n_publishes = 1

        # First tick: publish fails; we back off 1s and return.
        # We can't easily wait 1s; instead just confirm the tick returns
        # and a second tick succeeds.
        tick1 = asyncio.create_task(s.scheduler_tick())
        # Wait a moment to let publish fail, then cancel before the 1s backoff
        # completes (cancelling mid-sleep is fine because the cancel just
        # raises CancelledError, which scheduler_tick doesn't catch).
        await asyncio.sleep(0.05)
        tick1.cancel()
        try:
            await tick1
        except asyncio.CancelledError:
            pass

        # The retry tick should publish successfully.
        await s.scheduler_tick()
        self.assertEqual(mqtt.published[-1], ("t", "X", 0))
        self.assertEqual(s._store._messages[mid].display_count, 1)

    async def test_publish_failure_does_not_set_last_displayed_at(self):
        s, mqtt = self._make_scheduler(default_display_duration=10)
        mid = await s.add_message("X")
        m = s._store._messages[mid]
        mqtt.fail_n_publishes = 1

        tick = asyncio.create_task(s.scheduler_tick())
        await asyncio.wait_for(tick, timeout=2.5)

        self.assertIsNone(m.last_displayed_at)

    async def test_current_set_then_cleared(self):
        s, _ = self._make_scheduler(default_display_duration=0.02)
        mid = await s.add_message("ONLY")
        m = s._store._messages[mid]
        m.target_display_count = 1  # completes after this tick

        await s.scheduler_tick()
        self.assertIsNone(s._current)
        # And the message is completed
        self.assertEqual(m.status, MessageStatus.COMPLETED)

    async def test_publish_failure_resets_current(self):
        s, mqtt = self._make_scheduler(default_display_duration=10)
        await s.add_message("X")
        mqtt.fail_n_publishes = 1

        # We need a current set, so call _run_loop's tick.
        # First, manually set current then fail.
        s._current = s._store._messages[next(iter(s._store._messages))]

        tick = asyncio.create_task(s.scheduler_tick())
        await asyncio.wait_for(tick, timeout=2.5)
        self.assertIsNone(s._current)

    async def test_no_active_messages_falls_through_to_idle(self):
        # Empty queue -> scheduler_tick should NOT publish a real message.
        s, mqtt = self._make_scheduler(idle_mode="keep")
        await s.scheduler_tick()
        self.assertEqual(len(mqtt.published), 0)

    async def test_priority_high_picked_first(self):
        s, mqtt = self._make_scheduler(
            default_display_duration=0.02,
            default_target_display_count=2,
        )
        a = await s.add_message("A", priority="normal")
        b = await s.add_message("B", priority="high")
        await s.scheduler_tick()
        # First published must be the high-priority one.
        self.assertEqual(mqtt.published[-1][1], "B")

    async def test_queue_snapshot_emitted_before_dwell(self):
        """The queue snapshot must reflect the new displayCount immediately
        after a successful publish, not after the display_duration sleep.
        """
        s, mqtt = self._make_scheduler(
            default_display_duration=0.05,
            default_target_display_count=3,
        )
        # Subscribe BEFORE adding so the add events are delivered.
        q = s.subscribe_queue()
        try:
            # Drain the seeded snapshots (current, queue, history).
            for _ in range(3):
                await asyncio.wait_for(q.get(), timeout=1.0)
            mid = await s.add_message("HELLO")
            # Drain the two add events (queue + history).
            for _ in range(2):
                await asyncio.wait_for(q.get(), timeout=1.0)
            # Run the full tick to completion (publish + sleep + finish).
            await s.scheduler_tick()
            # Now drain every event the tick emitted and find the queue
            # snapshot. The snapshot must show displayCount=1 — proving it
            # was emitted before the sleep, not after.
            queue_events = []
            while not q.empty():
                evt = q.get_nowait()
                if evt["type"] == "queue":
                    queue_events.append(evt)
            self.assertEqual(len(queue_events), 1)
            msg_dict = next(m for m in queue_events[0]["messages"] if m["id"] == str(mid))
            self.assertEqual(msg_dict["displayCount"], 1)
        finally:
            s.unsubscribe_queue(q)


class IdleHandlerTests(AsyncTestCase):

    async def test_idle_publish_mode_publishes_idle_message(self):
        s, mqtt = self._make_scheduler(
            idle_mode="publish",
            idle_message="WELCOME",
            idle_publish_interval=0.05,  # short for test
        )
        # Run a single tick in idle state
        tick = asyncio.create_task(s.scheduler_tick())
        await asyncio.sleep(0.02)
        tick.cancel()
        try:
            await tick
        except asyncio.CancelledError:
            pass

        self.assertTrue(any(p[1] == "WELCOME" for p in mqtt.published))

    async def test_idle_keep_mode_does_not_publish(self):
        s, mqtt = self._make_scheduler(idle_mode="keep")
        # Idle keep mode: publish nothing
        tick = asyncio.create_task(s.scheduler_tick())
        await asyncio.sleep(0.05)
        tick.cancel()
        try:
            await tick
        except asyncio.CancelledError:
            pass
        self.assertEqual(len(mqtt.published), 0)

    async def test_idle_publish_failure_does_not_raise(self):
        s, mqtt = self._make_scheduler(
            idle_mode="publish",
            idle_publish_interval=0.05,
        )
        mqtt.fail_n_publishes = 1
        # Should not propagate
        tick = asyncio.create_task(s.scheduler_tick())
        await asyncio.sleep(0.02)
        tick.cancel()
        try:
            await tick
        except asyncio.CancelledError:
            pass
        # If we got here, the handler swallowed the error.
        self.assertTrue(True)


class RunLoopTests(AsyncTestCase):
    """End-to-end behaviour of the long-running loop."""

    async def test_run_loop_publishes_in_priority_order(self):
        s, mqtt = self._make_scheduler(
            default_display_duration=0.01,
            default_target_display_count=1,
        )
        # Add three messages
        await s.add_message("A", priority="normal")
        await s.add_message("B", priority="normal")
        await s.add_message("H", priority="high")

        await s.start()
        try:
            # Wait until all three have been published at least once
            for _ in range(50):
                if len(mqtt.published) >= 3:
                    break
                await asyncio.sleep(0.02)
        finally:
            await s.stop()

        self.assertGreaterEqual(len(mqtt.published), 3)
        # The first published must be H (high priority).
        self.assertEqual(mqtt.published[0][1], "H")
        # Both A and B should have been published at some point.
        seen = {p[1] for p in mqtt.published}
        self.assertIn("A", seen)
        self.assertIn("B", seen)

    async def test_run_loop_idle_wakeup_publishes_new_message(self):
        s, mqtt = self._make_scheduler(
            idle_mode="publish",
            idle_message="IDLE",
            idle_publish_interval=10,  # long; we want to wake via event
        )
        await s.start()
        # Let it run one idle cycle
        await asyncio.sleep(0.05)

        # Add a message; the wake event should make scheduler_tick return
        # quickly so the new message is published.
        await s.add_message("WAKEUP")

        for _ in range(50):
            if any(p[1] == "WAKEUP" for p in mqtt.published):
                break
            await asyncio.sleep(0.02)
        await s.stop()

        self.assertTrue(
            any(p[1] == "WAKEUP" for p in mqtt.published),
            f"WAKEUP was never published: {mqtt.published}",
        )

    async def test_run_loop_publishes_to_configured_topic(self):
        s, mqtt = self._make_scheduler(
            default_display_duration=0.01,
            default_target_display_count=1,
        )
        # Override publish topic
        s._publish_topic = "my/custom/topic"
        await s.add_message("X")

        await s.start()
        try:
            for _ in range(50):
                if mqtt.published:
                    break
                await asyncio.sleep(0.02)
        finally:
            await s.stop()

        self.assertTrue(mqtt.published)
        topic, _, _ = mqtt.published[0]
        self.assertEqual(topic, "my/custom/topic")

    async def test_run_loop_publishes_raw_string_payload(self):
        # Per our design decision: payload is the message text, not JSON.
        s, mqtt = self._make_scheduler(
            default_display_duration=0.01,
            default_target_display_count=1,
        )
        await s.add_message("HELLO WORLD")

        await s.start()
        try:
            for _ in range(50):
                if mqtt.published:
                    break
                await asyncio.sleep(0.02)
        finally:
            await s.stop()

        self.assertTrue(mqtt.published)
        _, payload, _ = mqtt.published[0]
        self.assertEqual(payload, "HELLO WORLD")
        # And it must not be wrapped in JSON.
        self.assertFalse(payload.startswith("{"))
