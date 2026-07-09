"""POST /v1/pods/detect — engagement-pod clusters from comment events."""

from __future__ import annotations

from datetime import UTC, datetime

import numpy as np
from fastapi import APIRouter

from app.models.pods import detect_pods
from app.registry import get_registry
from app.schemas import Pod, PodsDetectRequest, PodsDetectResponse

router = APIRouter(prefix="/v1/pods", tags=["pods"])

# Number of analyzed commenters at which volume no longer limits confidence.
_VOLUME_TARGET = 30.0
_CONFIDENCE_CAP = 0.6
_CONFIDENCE_FLOOR = 0.15


@router.post("/detect", response_model=PodsDetectResponse)
def detect(request: PodsDetectRequest) -> PodsDetectResponse:
    results, analyzed = detect_pods(
        request.events, request.window_minutes, request.min_pod_size
    )
    pods = [
        Pod(
            members=r.members,
            size=len(r.members),
            cohesion=round(r.cohesion, 6),
            confidence=round(r.confidence, 6),
        )
        for r in results
    ]

    volume_factor = min(analyzed / _VOLUME_TARGET, 1.0)
    detection_factor = (
        float(np.mean([r.confidence for r in results])) if results else 0.0
    )
    span = _CONFIDENCE_CAP - _CONFIDENCE_FLOOR
    raw = _CONFIDENCE_FLOOR + span * 0.5 * (volume_factor + detection_factor)
    confidence = round(min(raw, _CONFIDENCE_CAP), 4)
    return PodsDetectResponse(
        pods=pods,
        commenters_analyzed=analyzed,
        confidence=confidence,
        model_version=get_registry().active_version(),
        generated_at=datetime.now(UTC),
    )
