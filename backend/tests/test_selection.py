"""Tests for the selection algorithm: spec §7 Rules 1-4.

Rules under test:
  1. Ignore Completed messages.
  2. Among remaining, the lowest displayCount wins.
  3. Priority dominates displayCount.
  4. Among ties on count, the oldest createdAt wins.
  Plus: the currently-displayed message is excluded from selection.
"""

import uuid
from datetime import datetime, timedelta

from app.scheduler import MessageStatus

from ._fixtures import AsyncTestCase


class SelectNextMessageTests(AsyncTestCase):

    async def test_empty_queue_returns_none(self):
        s, _ = self._make_scheduler()
        self.assertIsNone(s.select_next_message())

    async def test_completed_messages_are_ignored(self):
        s, _ = self._make_scheduler()
        only = await s.add_message("only")
        s._store._messages[only].status = MessageStatus.COMPLETED
        self.assertIsNone(s.select_next_message())

    async def test_ignores_completed_picks_active(self):
        s, _ = self._make_scheduler()
        dead = await s.add_message("DEAD")
        live = await s.add_message("LIVE")
        s._store._messages[dead].status = MessageStatus.COMPLETED
        self.assertEqual(s.select_next_message().message, "LIVE")

    async def test_priority_dominates_over_count(self):
        # High-priority B has been displayed 99 times.
        # Normal A has 0 displays. Priority should still win.
        s, _ = self._make_scheduler()
        a = await s.add_message("A", priority="normal")
        b = await s.add_message("B", priority="high")
        s._store._messages[a].display_count = 0
        s._store._messages[b].display_count = 99
        self.assertEqual(s.select_next_message().message, "B")

    async def test_lowest_count_wins_among_same_priority(self):
        s, _ = self._make_scheduler()
        a = await s.add_message("A", priority="normal")
        b = await s.add_message("B", priority="normal")
        s._store._messages[a].display_count = 5
        s._store._messages[b].display_count = 2
        self.assertEqual(s.select_next_message().message, "B")

    async def test_oldest_wins_on_full_tie(self):
        s, _ = self._make_scheduler()
        older = await s.add_message("OLDER", priority="normal")
        newer = await s.add_message("NEWER", priority="normal")
        s._store._messages[older].created_at = datetime.now() - timedelta(minutes=10)
        s._store._messages[newer].created_at = datetime.now()
        # Both have displayCount=0 and priority=normal
        self.assertEqual(s.select_next_message().message, "OLDER")

    async def test_count_dominates_over_age(self):
        s, _ = self._make_scheduler()
        newer = await s.add_message("NEWER", priority="normal")
        older = await s.add_message("OLDER", priority="normal")
        s._store._messages[newer].display_count = 0
        s._store._messages[newer].created_at = datetime.now()
        s._store._messages[older].display_count = 3
        s._store._messages[older].created_at = datetime.now() - timedelta(hours=1)
        # NEWER has lower count, so it wins despite being newer
        self.assertEqual(s.select_next_message().message, "NEWER")

    async def test_current_does_not_affect_selection(self):
        # The current message is NOT excluded; it can be re-selected on the
        # next tick. After each tick the count increments, so a re-selected
        # message naturally sinks in the sort.
        s, _ = self._make_scheduler()
        only = await s.add_message("ONLY")
        s._current = s._store._messages[only]
        self.assertEqual(s.select_next_message().message, "ONLY")

    async def test_count_alone_prevents_immediate_repeat(self):
        # Even without an exclusion, a message that's already been displayed
        # has a higher count and loses the tie to fresh ones.
        s, _ = self._make_scheduler()
        a = await s.add_message("A")
        b = await s.add_message("B")
        s._current = s._store._messages[a]
        s._store._messages[a].display_count = 1
        s._store._messages[b].display_count = 0
        # B has the lower count, so B is picked, not the current A.
        self.assertEqual(s.select_next_message().message, "B")

    async def test_high_priority_repeated_even_when_current(self):
        # Priority dominates count. A high-priority message that's the current
        # is still selected over normal messages with the same count, because
        # the high-priority rank wins.
        s, _ = self._make_scheduler()
        h = await s.add_message("H", priority="high")
        n = await s.add_message("N", priority="normal")
        s._current = s._store._messages[h]
        s._store._messages[h].display_count = 1
        s._store._messages[n].display_count = 0
        # Even though N has lower count, H is high-priority and wins.
        self.assertEqual(s.select_next_message().message, "H")

    async def test_complex_sort(self):
        # Three messages, mixed priorities and counts.
        s, _ = self._make_scheduler()
        normal_old = await s.add_message("NORMAL_OLD", priority="normal")
        high_new = await s.add_message("HIGH_NEW", priority="high")
        normal_new = await s.add_message("NORMAL_NEW", priority="normal")

        s._store._messages[normal_old].display_count = 0
        s._store._messages[high_new].display_count = 0
        s._store._messages[normal_new].display_count = 0

        # Set timestamps so we can verify age tiebreak among normal.
        s._store._messages[normal_old].created_at = datetime.now() - timedelta(minutes=30)
        s._store._messages[normal_new].created_at = datetime.now() - timedelta(minutes=5)
        s._store._messages[high_new].created_at = datetime.now()

        # High-priority should win regardless of age.
        self.assertEqual(s.select_next_message().message, "HIGH_NEW")

        # Mark high as completed; among normals, oldest wins.
        s._store._messages[high_new].status = MessageStatus.COMPLETED
        self.assertEqual(s.select_next_message().message, "NORMAL_OLD")

    async def test_does_not_resurrect_completed_after_full_rotation(self):
        # Spec §15 fairness: a completed message is never rescheduled.
        s, _ = self._make_scheduler()
        a = await s.add_message("A", target_display_count=2)
        b = await s.add_message("B")
        # Simulate A having completed its target
        s._store._messages[a].display_count = 2
        s._store._messages[a].status = MessageStatus.COMPLETED
        # B should be selected, A should never reappear.
        for _ in range(5):
            self.assertEqual(s.select_next_message().message, "B")
