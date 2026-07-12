import asyncio
import logging
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles

from .config import settings
from .models import PublishRequest
from .mqtt_client import mqtt_client
from .scheduler import Scheduler
from .scheduler_api import router as scheduler_router, set_scheduler

logging.basicConfig(level=logging.ERROR)
logging.getLogger("backend.app").setLevel(logging.ERROR)
logging.getLogger("uvicorn.access").setLevel(logging.ERROR)
logging.getLogger("uvicorn.error").setLevel(logging.ERROR)
logging.getLogger("mqtt").setLevel(logging.ERROR)

logger = logging.getLogger(__name__)

STATIC_DIR = Path(__file__).resolve().parent.parent.parent / "frontend" / "static"

scheduler: Scheduler | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    from .config import CONFIG_FILE
    if CONFIG_FILE.exists():
        logger.info("Loading configuration from file: %s", CONFIG_FILE)
    else:
        logger.info("Configuration file not found at %s, using environment variables", CONFIG_FILE)
    logger.info("Configuration loaded:")
    logger.info("  MQTT_BROKER_HOST: %s", settings.mqtt_broker_host)
    logger.info("  MQTT_BROKER_PORT: %s", settings.mqtt_broker_port)
    logger.info("  MQTT_CLIENT_ID: %s", settings.mqtt_client_id)
    logger.info("  PUBLISH_TOPIC: %s", settings.publish_topic)
    logger.info("  SUBSCRIBE_TOPIC: %s", settings.subscribe_topic)
    logger.info("  DEFAULT_DISPLAY_DURATION: %s", settings.default_display_duration)
    logger.info("  DEFAULT_TARGET_DISPLAY_COUNT: %s", settings.default_target_display_count)
    logger.info("  IDLE_MESSAGE: %s", settings.idle_message)
    logger.info("  IDLE_MODE: %s", settings.idle_mode)

    global scheduler
    scheduler = Scheduler(
        mqtt=mqtt_client,
        publish_topic=settings.publish_topic,
        default_display_duration=settings.default_display_duration,
        default_target_display_count=settings.default_target_display_count,
        idle_message=settings.idle_message,
        idle_mode=settings.idle_mode,
        idle_publish_interval=settings.idle_publish_interval,
    )
    set_scheduler(scheduler)

    await mqtt_client.start()
    if settings.scheduler_enabled:
        await scheduler.start()
    try:
        yield
    finally:
        if scheduler is not None:
            await scheduler.stop()
        await mqtt_client.stop()


app = FastAPI(lifespan=lifespan)
app.include_router(scheduler_router)


@app.get("/api/config")
async def get_config():
    return {
        "publish_topic": settings.publish_topic,
        "subscribe_topic": settings.subscribe_topic,
        "broker_host": settings.mqtt_broker_host,
        "connected": mqtt_client.connected,
        "default_display_duration": settings.default_display_duration,
        "default_target_display_count": settings.default_target_display_count,
        "idle_message": settings.idle_message,
        "idle_mode": settings.idle_mode,
        "scheduler_enabled": settings.scheduler_enabled,
    }


@app.post("/api/publish")
async def publish(req: PublishRequest, request: Request):
    if scheduler is None:
        raise HTTPException(status_code=503, detail="scheduler not ready")
    text = (req.text or req.payload or "").strip()
    if not text:
        raise HTTPException(status_code=400, detail="text must be non-empty")

    user_email = request.headers.get("Cf-Access-Authenticated-User-Email", "")
    user = user_email.split("@")[0] if user_email else "unknown"

    try:
        mid = await scheduler.add_message(
            text=text,
            target_display_count=req.target_display_count,
            display_duration=req.display_duration,
            priority=req.priority,
            user=user,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))

    return {"status": "ok", "id": str(mid)}


app.mount("/static", StaticFiles(directory=str(STATIC_DIR)), name="static")


@app.get("/")
async def index():
    return FileResponse(str(STATIC_DIR / "index.html"))
