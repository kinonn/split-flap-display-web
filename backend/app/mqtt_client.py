import asyncio
import logging
from collections import deque
from typing import Set

from asyncio_mqtt import Client, MqttError, Topic

from .config import settings

logger = logging.getLogger(__name__)

MAX_HISTORY = 500


class MQTTClient:
    def __init__(self):
        self._client: Client | None = None
        self._connected = False
        self._history: deque = deque(maxlen=MAX_HISTORY)
        self._subscribers: Set[asyncio.Queue] = set()
        self._task: asyncio.Task | None = None
        self._latest_message: str | None = None
        self._current_subscribers: Set[asyncio.Queue] = set()

    @property
    def connected(self) -> bool:
        return self._connected

    def get_history(self) -> list:
        return list(self._history)

    def get_latest_message(self) -> str | None:
        """Get the most recent message payload received from the subscribe topic."""
        return self._latest_message

    def subscribe_queue(self) -> asyncio.Queue:
        q: asyncio.Queue = asyncio.Queue(maxsize=MAX_HISTORY)
        for msg in self._history:
            q.put_nowait(msg)
        self._subscribers.add(q)
        return q

    def unsubscribe_queue(self, q: asyncio.Queue):
        self._subscribers.discard(q)

    def subscribe_current(self) -> asyncio.Queue:
        """Subscribe to "current" events when the displayed message changes."""
        q: asyncio.Queue = asyncio.Queue(maxsize=100)
        if self._latest_message is not None:
            q.put_nowait({"type": "current", "message": {"message": self._latest_message}})
        self._current_subscribers.add(q)
        return q

    def unsubscribe_current(self, q: asyncio.Queue) -> None:
        self._current_subscribers.discard(q)

    async def start(self):
        self._task = asyncio.create_task(self._run())

    async def stop(self):
        if self._task:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass

    async def _run(self):
        first_attempt = True
        while True:
            try:
                async with Client(
                    hostname=settings.mqtt_broker_host,
                    port=settings.mqtt_broker_port,
                    client_id=settings.mqtt_client_id,
                    keepalive=30,
                ) as client:
                    self._client = client
                    self._connected = True
                    logger.info("Connected to MQTT broker %s:%s",
                                settings.mqtt_broker_host, settings.mqtt_broker_port)
                    await client.subscribe(settings.subscribe_topic)
                    logger.info("Subscribed to topic: %s", settings.subscribe_topic)
                    first_attempt = False
                    subscribe_topic = Topic(settings.subscribe_topic)
                    async with client.messages() as messages:
                        async for message in messages:
                            if not subscribe_topic.matches(message.topic):
                                continue
                            payload = message.payload.decode("utf-8", errors="replace")
                            self._latest_message = payload
                            msg = {"topic": str(message.topic), "payload": payload}
                            self._history.append(msg)
                            # Notify "current" subscribers that the displayed message changed
                            for q in list(self._current_subscribers):
                                try:
                                    q.put_nowait({"type": "current", "message": {"message": payload}})
                                except asyncio.QueueFull:
                                    try:
                                        q.get_nowait()
                                    except asyncio.QueueEmpty:
                                        pass
                                    try:
                                        q.put_nowait({"type": "current", "message": {"message": payload}})
                                    except asyncio.QueueFull:
                                        pass
                            for q in list(self._subscribers):
                                try:
                                    q.put_nowait(msg)
                                except asyncio.QueueFull:
                                    try:
                                        q.get_nowait()
                                    except asyncio.QueueEmpty:
                                        pass
                                    q.put_nowait(msg)
            except MqttError as e:
                self._connected = False
                self._client = None
                if first_attempt:
                    logger.warning("MQTT connection error: %s. Retrying in 5s...", e)
                else:
                    logger.debug("MQTT connection error: %s. Retrying in 5s...", e)
                await asyncio.sleep(5)
            except asyncio.CancelledError:
                self._connected = False
                self._client = None
                raise

    async def publish(self, topic: str, payload: str, qos: int = 0):
        if not self._connected or not self._client:
            raise RuntimeError("MQTT not connected")
        await self._client.publish(topic, payload.encode("utf-8"), qos=qos)


mqtt_client = MQTTClient()
