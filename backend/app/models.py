from datetime import datetime
from typing import Literal, Optional

from pydantic import BaseModel


PriorityStr = Literal["normal", "high"]


class PublishRequest(BaseModel):
    payload: str
    topic: Optional[str] = None
    qos: int = 0
    priority: PriorityStr = "normal"


class AddMessageRequest(BaseModel):
    text: str
    target_display_count: Optional[int] = None
    display_duration: Optional[int] = None
    priority: PriorityStr = "normal"


class MessageResponse(BaseModel):
    id: str
    message: str
    createdAt: datetime
    status: str
    displayDuration: int
    targetDisplayCount: int
    displayCount: int
    lastDisplayedAt: Optional[datetime] = None
    priority: PriorityStr


class ReceivedMessage(BaseModel):
    topic: str
    payload: str
