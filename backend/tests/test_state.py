"""Tests for Scheduler.state, Scheduler.high_priority_count, and accessors."""

import asyncio

from app.scheduler import MessageStatus

from ._fixtures import AsyncTestCase


class StateTests(AsyncTestCase):

    async def test_starts_idle(self):
        s, _ = self._make_scheduler()
        self.assertEqual(s.state(), "Idle")

    async def test_active_when_messages_pending(self):
        s, _ = self._make_scheduler()
        await s.add_message("X")
        self.assertEqual(s.state(), "Active")

    async def test_active_when_current_set_even_if_no_others(self):
        s, _ = self._make_scheduler()
        # The state() helper checks both current and active count.
        # If we have a current but no other active, it's still Active.
        from ._fixtures import make_message
        from app.scheduler import MessageStatus
        m = make_message(status=MessageStatus.ACTIVE)
        s._store._messages[m.id] = m
        s._current = m
        self.assertEqual(s.state(), "Active")

    async def test_returns_to_idle_when_all_completed(self):
        s, _ = self._make_scheduler()
        a = await s.add_message("A")
        b = await s.add_message("B")
        for mid in (a, b):
            s._store._messages[mid].status = MessageStatus.COMPLETED
        s._current = None
        self.assertEqual(s.state(), "Idle")

    async def test_active_when_some_completed_some_pending(self):
        s, _ = self._make_scheduler()
        a = await s.add_message("A")
        b = await s.add_message("B")
        s._store._messages[a].status = MessageStatus.COMPLETED
        self.assertEqual(s.state(), "Active")


class HighPriorityCountTests(AsyncTestCase):

    async def test_zero_when_no_messages(self):
        s, _ = self._make_scheduler()
        self.assertEqual(s.high_priority_count(), 0)

    async def test_counts_only_high_priority_active(self):
        s, _ = self._make_scheduler()
        await s.add_message("N1", priority="normal")
        await s.add_message("H1", priority="high")
        await s.add_message("H2", priority="high")
        self.assertEqual(s.high_priority_count(), 2)

    async def test_excludes_completed_high_priority(self):
        s, _ = self._make_scheduler()
        h = await s.add_message("H", priority="high")
        await s.add_message("N", priority="normal")
        s._store._messages[h].status = MessageStatus.COMPLETED
        self.assertEqual(s.high_priority_count(), 0)


class GetActiveMessagesTests(AsyncTestCase):

    async def test_returns_only_active(self):
        s, _ = self._make_scheduler()
        a = await s.add_message("A")
        b = await s.add_message("B")
        s._store._messages[a].status = MessageStatus.COMPLETED
        active = s.get_active_messages()
        msgs = {m.message for m in active}
        self.assertEqual(msgs, {"B"})

    async def test_includes_pending_and_active(self):
        s, _ = self._make_scheduler()
        await s.add_message("P")  # Pending
        b = await s.add_message("A")
        s._store._messages[b].status = MessageStatus.ACTIVE
        active = s.get_active_messages()
        self.assertEqual(len(active), 2)


class GetCurrentMessageTests(AsyncTestCase):

    async def test_none_initially(self):
        s, _ = self._make_scheduler()
        self.assertIsNone(s.get_current_message())

    async def test_returns_current(self):
        s, _ = self._make_scheduler()
        mid = await s.add_message("X")
        s._current = s._store._messages[mid]
        self.assertIs(s.get_current_message(), s._store._messages[mid])
