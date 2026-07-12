"""Unit tests for the scheduler module.

Run with:
    python -m unittest discover -s backend/tests -t .

Or, with pytest if available:
    pytest backend/tests
"""

import asyncio
import uuid
from datetime import datetime, timedelta

from ._fixtures import AsyncTestCase, FakeMQTT, make_message


class MessageTests(AsyncTestCase):
    """to_dict() shape and defaults."""

    def test_to_dict_contains_all_spec_fields(self):
        m = make_message(message="HI")
        d = m.to_dict()
        self.assertEqual(set(d.keys()), {
            "id", "message", "createdAt", "status",
            "displayDuration", "targetDisplayCount",
            "displayCount", "lastDisplayedAt", "priority",
        })
        self.assertEqual(d["message"], "HI")
        self.assertEqual(d["status"], "Pending")
        self.assertEqual(d["displayCount"], 0)
        self.assertIsNone(d["lastDisplayedAt"])
        self.assertEqual(d["priority"], "normal")

    def test_to_dict_serializes_timestamps(self):
        ts = datetime(2026, 1, 2, 3, 4, 5)
        m = make_message(created_at=ts, last_displayed_at=ts)
        d = m.to_dict()
        self.assertEqual(d["createdAt"], ts.isoformat())
        self.assertEqual(d["lastDisplayedAt"], ts.isoformat())


class MessageStoreTests(AsyncTestCase):
    """CRUD + lock-protected state transitions on the in-memory store."""

    async def test_add_and_get(self):
        from app.scheduler import MessageStore
        store = MessageStore()
        m = make_message()
        await store.add(m)
        self.assertIs(await store.get(m.id), m)

    async def test_get_unknown_returns_none(self):
        from app.scheduler import MessageStore
        store = MessageStore()
        self.assertIsNone(await store.get(uuid.uuid4()))

    async def test_list_active_excludes_completed(self):
        from app.scheduler import MessageStore, MessageStatus
        store = MessageStore()
        a = make_message(message="A")
        b = make_message(message="B")
        c = make_message(message="C", status=MessageStatus.COMPLETED)
        for m in (a, b, c):
            await store.add(m)
        active = await store.list_active()
        msgs = {m.message for m in active}
        self.assertEqual(msgs, {"A", "B"})

    async def test_list_all_returns_everything(self):
        from app.scheduler import MessageStore, MessageStatus
        store = MessageStore()
        await store.add(make_message(message="A"))
        await store.add(make_message(message="B", status=MessageStatus.COMPLETED))
        self.assertEqual(len(await store.list_all()), 2)

    async def test_mark_completed_marks_and_returns_true(self):
        from app.scheduler import MessageStore, MessageStatus
        store = MessageStore()
        m = make_message()
        await store.add(m)
        ok = await store.mark_completed(m.id)
        self.assertTrue(ok)
        self.assertEqual(m.status, MessageStatus.COMPLETED)

    async def test_mark_completed_unknown_returns_false(self):
        from app.scheduler import MessageStore
        store = MessageStore()
        self.assertFalse(await store.mark_completed(uuid.uuid4()))

    async def test_update_fields_modifies_attributes(self):
        from app.scheduler import MessageStore
        store = MessageStore()
        m = make_message()
        await store.add(m)
        await store.update_fields(m.id, display_count=2, last_displayed_at=datetime(2026, 5, 5))
        self.assertEqual(m.display_count, 2)
        self.assertEqual(m.last_displayed_at, datetime(2026, 5, 5))

    async def test_update_fields_unknown_id_is_noop(self):
        from app.scheduler import MessageStore
        store = MessageStore()
        # Should not raise.
        await store.update_fields(uuid.uuid4(), display_count=99)
