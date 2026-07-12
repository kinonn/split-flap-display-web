from datetime import datetime
from typing import Literal, Optional

from pydantic import BaseModel


PriorityStr = Literal["normal", "high"]


class PublishRequest(BaseModel):
    text: Optional[str] = None
    payload: Optional[str] = None
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
