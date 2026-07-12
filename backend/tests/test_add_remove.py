"""Tests for Scheduler.add_message and Scheduler.remove_message."""

import asyncio
import uuid

from app.scheduler import MessageStatus, PRIORITY_RANK

from ._fixtures import AsyncTestCase


class AddMessageTests(AsyncTestCase):

    async def test_returns_uuid(self):
        s, _ = self._make_scheduler()
        mid = await s.add_message("HELLO")
        self.assertIsInstance(mid, uuid.UUID)

    async def test_uses_config_defaults(self):
        s, _ = self._make_scheduler(
            default_display_duration=7,
            default_target_display_count=4,
        )
        mid = await s.add_message("HELLO")
        m = s._store._messages[mid]
        self.assertEqual(m.display_duration, 7)
        self.assertEqual(m.target_display_count, 4)

    async def test_overrides_take_precedence_over_defaults(self):
        s, _ = self._make_scheduler(
            default_display_duration=7,
            default_target_display_count=4,
        )
        mid = await s.add_message("HELLO", target_display_count=99, display_duration=2)
        m = s._store._messages[mid]
        self.assertEqual(m.display_duration, 2)
        self.assertEqual(m.target_display_count, 99)

    async def test_starts_pending_with_zero_count(self):
        s, _ = self._make_scheduler()
        mid = await s.add_message("HELLO")
        m = s._store._messages[mid]
        self.assertEqual(m.status, MessageStatus.PENDING)
        self.assertEqual(m.display_count, 0)
        self.assertIsNone(m.last_displayed_at)

    async def test_priority_normal_is_default(self):
        s, _ = self._make_scheduler()
        mid = await s.add_message("HELLO")
        self.assertEqual(s._store._messages[mid].priority, "normal")

    async def test_priority_high_persists(self):
        s, _ = self._make_scheduler()
        mid = await s.add_message("HELLO", priority="high")
        self.assertEqual(s._store._messages[mid].priority, "high")

    async def test_priority_rank_values(self):
        # Sanity check on the lookup table used by select_next_message.
        self.assertEqual(PRIORITY_RANK["normal"], 0)
        self.assertEqual(PRIORITY_RANK["high"], 1)
        self.assertGreater(PRIORITY_RANK["high"], PRIORITY_RANK["normal"])

    async def test_empty_text_raises(self):
        s, _ = self._make_scheduler()
        with self.assertRaises(ValueError):
            await s.add_message("")

    async def test_whitespace_only_text_raises(self):
        s, _ = self._make_scheduler()
        with self.assertRaises(ValueError):
            await s.add_message("   ")

    async def test_zero_target_raises(self):
        s, _ = self._make_scheduler()
        with self.assertRaises(ValueError):
            await s.add_message("X", target_display_count=0)

    async def test_negative_target_raises(self):
        s, _ = self._make_scheduler()
        with self.assertRaises(ValueError):
            await s.add_message("X", target_display_count=-1)

    async def test_zero_duration_raises(self):
        s, _ = self._make_scheduler()
        with self.assertRaises(ValueError):
            await s.add_message("X", display_duration=0)

    async def test_invalid_priority_raises(self):
        s, _ = self._make_scheduler()
        for bad in ("urgent", "low", "NORMAL", "HIGH", "", None):
            with self.subTest(priority=bad):
                with self.assertRaises(ValueError):
                    await s.add_message("X", priority=bad)

    async def test_add_notifies_subscribers(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        try:
            mid = await s.add_message("HELLO", priority="high")
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "queue")
            self.assertEqual(evt["message"]["id"], str(mid))
            self.assertEqual(evt["message"]["priority"], "high")
        finally:
            s.unsubscribe_queue(q)

    async def test_add_sets_wakeup_event(self):
        s, _ = self._make_scheduler()
        self.assertFalse(s._wakeup.is_set())
        await s.add_message("HELLO")
        self.assertTrue(s._wakeup.is_set())


class RemoveMessageTests(AsyncTestCase):

    async def test_marks_completed(self):
        from app.scheduler import MessageStatus
        s, _ = self._make_scheduler()
        mid = await s.add_message("X")
        ok = await s.remove_message(mid)
        self.assertTrue(ok)
        self.assertEqual(s._store._messages[mid].status, MessageStatus.COMPLETED)

    async def test_unknown_id_returns_false(self):
        s, _ = self._make_scheduler()
        self.assertFalse(await s.remove_message(uuid.uuid4()))

    async def test_remove_notifies_subscribers(self):
        s, _ = self._make_scheduler()
        q = s.subscribe_queue()
        try:
            mid = await s.add_message("X")
            # Drain the add event
            await asyncio.wait_for(q.get(), timeout=0.5)
            await s.remove_message(mid)
            evt = await asyncio.wait_for(q.get(), timeout=0.5)
            self.assertEqual(evt["type"], "queue")
            self.assertEqual(evt["removed"], str(mid))
        finally:
            s.unsubscribe_queue(q)

    async def test_remove_does_not_advance_state(self):
        s, _ = self._make_scheduler()
        mid = await s.add_message("X")
        m = s._store._messages[mid]
        m.display_count = 2  # already partly through
        await s.remove_message(mid)
        # display_count is not touched by remove_message
        self.assertEqual(m.display_count, 2)
