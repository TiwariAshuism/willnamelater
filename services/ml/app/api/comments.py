"""POST /v1/comments/classify — rule-based comment-quality buckets."""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter

from app.features.comments import classify_comment, duplicate_norms
from app.registry import get_registry
from app.schemas import (
    CommentClassification,
    CommentLabel,
    CommentsClassifyRequest,
    CommentsClassifyResponse,
)

router = APIRouter(prefix="/v1/comments", tags=["comments"])

# Batch size at which volume no longer limits confidence.
_VOLUME_TARGET = 20.0
_CONFIDENCE_CAP = 0.6
_CONFIDENCE_FLOOR = 0.15


@router.post("/classify", response_model=CommentsClassifyResponse)
def classify(request: CommentsClassifyRequest) -> CommentsClassifyResponse:
    comments = request.comments
    dupes = duplicate_norms([c.text for c in comments])

    classifications: list[CommentClassification] = []
    item_confidences: list[float] = []
    low_quality = 0
    for item in comments:
        label, item_conf, signals = classify_comment(item.text, dupes)
        if label is not CommentLabel.genuine:
            low_quality += 1
        item_confidences.append(item_conf)
        classifications.append(
            CommentClassification(
                id=item.id,
                label=label,
                confidence=round(item_conf, 6),
                signals=signals,
            )
        )

    total = len(comments)
    low_quality_ratio = low_quality / total if total else 0.0

    if total:
        volume_factor = min(total / _VOLUME_TARGET, 1.0)
        mean_item = sum(item_confidences) / total
        confidence = min(
            _CONFIDENCE_FLOOR
            + (_CONFIDENCE_CAP - _CONFIDENCE_FLOOR) * 0.5 * (volume_factor + mean_item),
            _CONFIDENCE_CAP,
        )
    else:
        confidence = _CONFIDENCE_FLOOR

    return CommentsClassifyResponse(
        classifications=classifications,
        low_quality_ratio=round(low_quality_ratio, 6),
        confidence=round(confidence, 4),
        model_version=get_registry().active_version(),
        generated_at=datetime.now(UTC),
    )
