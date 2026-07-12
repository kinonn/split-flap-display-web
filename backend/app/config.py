from pathlib import Path
from pydantic_settings import BaseSettings

CONFIG_FILE = Path(__file__).resolve().parent.parent / "app.conf"


class Settings(BaseSettings):
    mqtt_broker_host: str = "localhost"
    mqtt_broker_port: int = 1883
    mqtt_client_id: str = "splitflap-web"
    publish_topic: str = "splitflap/splitflap/set"
    subscribe_topic: str = "splitflap/splitflap/state"

    default_display_duration: int = 10
    default_target_display_count: int = 3
    idle_message: str = "WELCOME"
    idle_mode: str = "publish"
    idle_publish_interval: int = 10
    scheduler_enabled: bool = True

    model_config = {
        "env_file": str(CONFIG_FILE),
        "env_file_encoding": "utf-8",
    }


settings = Settings()
