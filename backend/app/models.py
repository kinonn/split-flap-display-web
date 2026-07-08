from pydantic import BaseModel
from typing import Optional


class PublishRequest(BaseModel):
    topic: Optional[str] = None
    payload: str
    qos: int = 0


class ReceivedMessage(BaseModel):
    topic: str
    payload: str
