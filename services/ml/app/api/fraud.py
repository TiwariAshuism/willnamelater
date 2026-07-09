"""POST /v1/fraud/score — authenticity risk estimate for one account."""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter

from app.models.heuristics import score_fraud
from app.registry import get_registry
from app.schemas import FraudScoreRequest, FraudScoreResponse

router = APIRouter(prefix="/v1/fraud", tags=["fraud"])


@router.post("/score", response_model=FraudScoreResponse)
def score(request: FraudScoreRequest) -> FraudScoreResponse:
    result = score_fraud(request)
    return FraudScoreResponse(
        score=result.score,
        confidence=result.confidence,
        model_version=get_registry().active_version(),
        signals=result.signals,
        flags=result.flags,
        generated_at=datetime.now(UTC),
    )
