import asyncio
import json
import logging
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from sse_starlette.sse import EventSourceResponse

from .config import settings
from .models import PublishRequest
from .mqtt_client import mqtt_client

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

STATIC_DIR = Path(__file__).resolve().parent.parent.parent / "frontend" / "static"


@asynccontextmanager
async def lifespan(app: FastAPI):
    await mqtt_client.start()
    yield
    await mqtt_client.stop()


app = FastAPI(lifespan=lifespan)


@app.get("/api/config")
async def get_config():
    return {
        "publish_topic": settings.publish_topic,
        "subscribe_topic": settings.subscribe_topic,
        "broker_host": settings.mqtt_broker_host,
        "connected": mqtt_client.connected,
    }


@app.post("/api/publish")
async def publish(req: PublishRequest):
    topic = req.topic or settings.publish_topic
    try:
        await mqtt_client.publish(topic, req.payload, req.qos)
    except RuntimeError as e:
        raise HTTPException(status_code=503, detail=str(e))
    return {"status": "ok", "topic": topic}


@app.get("/api/stream")
async def stream():
    queue = mqtt_client.subscribe_queue()

    async def event_generator():
        try:
            while True:
                msg = await queue.get()
                yield {"event": "message", "data": json.dumps(msg)}
        except asyncio.CancelledError:
            mqtt_client.unsubscribe_queue(queue)
            raise

    return EventSourceResponse(event_generator())


app.mount("/static", StaticFiles(directory=str(STATIC_DIR)), name="static")


@app.get("/")
async def index():
    return FileResponse(str(STATIC_DIR / "index.html"))
