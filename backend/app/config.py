from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    mqtt_broker_host: str = "localhost"
    mqtt_broker_port: int = 1883
    mqtt_client_id: str = "splitflap-web"
    publish_topic: str = "splitflap/splitflap/set"
    subscribe_topic: str = "splitflap/splitflap/state"

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
